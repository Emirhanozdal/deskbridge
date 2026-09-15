/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2015 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * Restored for DeskBridge cross-screen drag-and-drop file transfer.
 */

#include "deskflow/FileChunk.h"

#include "base/Log.h"
#include "deskflow/ProtocolTypes.h"
#include "deskflow/ProtocolUtil.h"
#include "io/IStream.h"

#include <QString>
#include <cstring>
#include <limits>

namespace {

void clearCachedData(std::string &dataCached)
{
  dataCached.clear();
  dataCached.shrink_to_fit();
}

bool wouldExceed(size_t currentSize, size_t extraSize, size_t limit)
{
  return currentSize > limit || extraSize > limit - currentSize;
}

} // namespace

FileChunk::FileChunk(size_t size) : Chunk(size)
{
  m_dataSize = size - s_fileChunkMetaSize;
}

FileChunk::~FileChunk()
{
  // releasing the flow token (once the queue deletes this chunk after sending)
  // lets the background sender read and queue the next chunk -> bounded memory.
  if (m_flow) {
    m_flow->release();
  }
}

FileChunk *FileChunk::start(const std::string &size)
{
  const size_t sizeLength = size.size();
  auto *start = new FileChunk(sizeLength + s_fileChunkMetaSize);
  char *chunk = start->m_chunk;

  chunk[0] = ChunkType::DataStart;
  std::memcpy(&chunk[1], size.c_str(), sizeLength);
  chunk[sizeLength + s_fileChunkMetaSize - 1] = '\0';

  return start;
}

FileChunk *FileChunk::data(const std::string &data)
{
  const size_t dataSize = data.size();
  auto *chunk = new FileChunk(dataSize + s_fileChunkMetaSize);
  char *chunkData = chunk->m_chunk;

  chunkData[0] = ChunkType::DataChunk;
  std::memcpy(&chunkData[1], data.c_str(), dataSize);
  chunkData[dataSize + s_fileChunkMetaSize - 1] = '\0';

  return chunk;
}

FileChunk *FileChunk::end()
{
  auto *end = new FileChunk(s_fileChunkMetaSize);
  char *chunk = end->m_chunk;

  chunk[0] = ChunkType::DataEnd;
  chunk[s_fileChunkMetaSize - 1] = '\0';
  return end;
}

TransferState FileChunk::assemble(
    deskflow::IStream *stream, std::string &dataCached, FileChunkAssemblyState &state, size_t maxDataSize
)
{
  using enum TransferState;
  uint8_t mark;
  std::string data;
  auto reset = [&]() {
    state = {};
    clearCachedData(dataCached);
  };

  // kMsgDFileTransfer code has already been consumed; read mark + payload.
  if (!ProtocolUtil::readf(stream, kMsgDFileTransfer + 4, &mark, &data)) {
    reset();
    return Error;
  }

  if (mark == ChunkType::DataStart) {
    bool ok = false;
    const auto expected = QString::fromStdString(data).toULongLong(&ok);
    if (!ok || expected > std::numeric_limits<size_t>::max()) {
      LOG_ERR("file transfer invalid size header: %s", data.c_str());
      reset();
      return Error;
    }

    clearCachedData(dataCached);
    state.expectedSize = static_cast<size_t>(expected);
    state.active = true;

    if (state.expectedSize > maxDataSize) {
      LOG_ERR("file transfer size exceeds limit, size: %zu, limit: %zu", state.expectedSize, maxDataSize);
      reset();
      return Error;
    }

    LOG_DEBUG("start receiving file data, expected size=%zu", state.expectedSize);
    return Started;
  } else if (mark == ChunkType::DataChunk) {
    if (!state.active) {
      LOG_ERR("file data chunk before start");
      reset();
      return Error;
    }

    if (wouldExceed(dataCached.size(), data.size(), state.expectedSize)) {
      LOG_ERR(
          "file transfer size exceeds declared, size: %zu, declared: %zu", dataCached.size() + data.size(),
          state.expectedSize
      );
      reset();
      return Error;
    }

    dataCached.append(data);
    return InProgress;
  } else if (mark == ChunkType::DataEnd) {
    if (!state.active) {
      LOG_ERR("file end chunk before start");
      reset();
      return Error;
    }

    state.active = false;

    if (state.expectedSize != dataCached.size()) {
      LOG_ERR("corrupted file data, expected size=%zu actual size=%zu", state.expectedSize, dataCached.size());
      reset();
      return Error;
    }
    return Finished;
  }

  LOG_ERR("unknown file chunk mark");
  reset();
  return Error;
}

void FileChunk::send(deskflow::IStream *stream, void *data)
{
  const auto *fileData = static_cast<FileChunk *>(data);

  const char *chunk = fileData->m_chunk;
  uint8_t mark = chunk[0];
  std::string dataChunk(&chunk[1], fileData->m_dataSize);

  switch (mark) {
  case ChunkType::DataStart:
    LOG_DEBUG("sending file chunk start: size=%s", dataChunk.c_str());
    break;
  case ChunkType::DataChunk:
    LOG_VERBOSE("sending file chunk data: size=%zu", dataChunk.size());
    break;
  case ChunkType::DataEnd:
    LOG_DEBUG("sending file transfer finished");
    break;
  default:
    break;
  }

  ProtocolUtil::writef(stream, kMsgDFileTransfer, mark, &dataChunk);
}
