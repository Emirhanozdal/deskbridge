/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2013 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 */

#include "deskflow/StreamChunker.h"

#include "base/Event.h"
#include "base/IEventQueue.h"
#include "base/Log.h"
#include "deskflow/ClipboardChunk.h"
#include "deskflow/FileChunk.h"

#include <QString>
#include <algorithm>
#include <fstream>
#include <memory>
#include <thread>

static const size_t g_chunkSize = 512 * 1024; // 512kb

// how many chunks (~g_chunkSize each) may be queued for a single file transfer
// at once; bounds in-flight memory to a few MB regardless of file size.
static const ptrdiff_t g_fileFlowWindow = 8;

void StreamChunker::sendClipboard(
    const std::string_view &data, size_t size, ClipboardID id, uint32_t sequence, IEventQueue *events, void *eventTarget
)
{
  // send first message (data size)
  std::string dataSize = QString::number(size).toStdString();
  ClipboardChunk *sizeMessage = ClipboardChunk::start(id, sequence, dataSize);

  events->addEvent(Event(EventTypes::ClipboardSending, eventTarget, sizeMessage));

  // send clipboard chunk with a fixed size
  size_t sentLength = 0;
  size_t chunkSize = g_chunkSize;

  while (true) {
    // make sure we don't read too much from the mock data.
    if (sentLength + chunkSize > size) {
      chunkSize = size - sentLength;
    }

    std::string chunk(data.substr(sentLength, chunkSize).data(), chunkSize);
    ClipboardChunk *dataChunk = ClipboardChunk::data(id, sequence, chunk);

    events->addEvent(Event(EventTypes::ClipboardSending, eventTarget, dataChunk));

    sentLength += chunkSize;
    if (sentLength == size) {
      break;
    }
  }

  // send last message
  ClipboardChunk *end = ClipboardChunk::end(id, sequence);

  events->addEvent(Event(EventTypes::ClipboardSending, eventTarget, end));

  LOG_DEBUG("sent clipboard size=%d", sentLength);
}

void StreamChunker::sendFile(const std::string &filename, IEventQueue *events, void *eventTarget)
{
  // Read and stream the file on a background thread so the input event loop is
  // never blocked, and gate the number of in-flight chunks with a semaphore so
  // memory use stays bounded (a few MB) no matter how large the file is. Only
  // the event thread ever writes to the network stream (via the FileChunkSending
  // handler); this thread just reads the file and queues chunks.
  std::thread([filename, events, eventTarget]() {
    std::ifstream file(filename, std::ios::binary | std::ios::ate);
    if (!file.is_open()) {
      LOG_ERR("drag file transfer: cannot open file: %s", filename.c_str());
      return;
    }
    const std::streamoff fileSize = file.tellg();
    if (fileSize < 0) {
      LOG_ERR("drag file transfer: cannot size file: %s", filename.c_str());
      return;
    }
    file.seekg(0, std::ios::beg);
    const auto size = static_cast<size_t>(fileSize);

    auto flow = std::make_shared<FileFlowToken>(g_fileFlowWindow);
    auto queue = [&](FileChunk *chunk) {
      flow->acquire(); // blocks once g_fileFlowWindow chunks are still in flight
      chunk->setFlowToken(flow);
      events->addEvent(Event(EventTypes::FileChunkSending, eventTarget, chunk));
    };

    // first message: total size as a decimal string
    queue(FileChunk::start(QString::number(static_cast<qulonglong>(size)).toStdString()));

    size_t sentLength = 0;
    std::string buffer(g_chunkSize, '\0');
    while (sentLength < size) {
      const size_t chunkSize = std::min(g_chunkSize, size - sentLength);
      file.read(buffer.data(), static_cast<std::streamsize>(chunkSize));
      if (static_cast<size_t>(file.gcount()) != chunkSize) {
        LOG_ERR("drag file transfer: short read on %s", filename.c_str());
        return;
      }
      queue(FileChunk::data(buffer.substr(0, chunkSize)));
      sentLength += chunkSize;
    }

    // last message
    queue(FileChunk::end());

    LOG_DEBUG("streamed file '%s' size=%zu", filename.c_str(), sentLength);
  }).detach();
}
