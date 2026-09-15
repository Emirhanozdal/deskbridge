/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2013 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 */

#include "server/ClientProxy1_5.h"

#include "base/IEventQueue.h"
#include "base/Log.h"
#include "deskflow/FileChunk.h"
#include "deskflow/ProtocolTypes.h"
#include "deskflow/ProtocolUtil.h"
#include "deskflow/StreamChunker.h"
#include "io/IStream.h"
#include "server/Server.h"

#include <cstring>

//
// ClientProxy1_5
//

ClientProxy1_5::ClientProxy1_5(const std::string &name, deskflow::IStream *stream, Server *server, IEventQueue *events)
    : ClientProxy1_4(name, stream, server, events),
      m_events(events)
{
  // Stream drag-and-drop file chunks queued by StreamChunker::sendFile out to
  // this client. Mirrors ClientProxy1_6's clipboard chunk handler.
  m_events->addHandler(EventTypes::FileChunkSending, this, [this](const auto &e) {
    FileChunk::send(getStream(), e.getDataObject());
  });
}

ClientProxy1_5::~ClientProxy1_5()
{
  m_events->removeHandler(EventTypes::FileChunkSending, this);
}

void ClientProxy1_5::sendDragInfo(uint32_t fileCount, const char *info, size_t size)
{
  std::string data(info, size);
  LOG_DEBUG("sending drag info to \"%s\": %u file(s), %zu bytes", getName().c_str(), fileCount, size);
  ProtocolUtil::writef(getStream(), kMsgDDragInfo, fileCount, &data);
}

void ClientProxy1_5::fileChunkSending(uint8_t mark, char *data, size_t dataSize)
{
  std::string chunk(data, dataSize);
  ProtocolUtil::writef(getStream(), kMsgDFileTransfer, mark, &chunk);
}

bool ClientProxy1_5::parseMessage(const uint8_t *code)
{
  if (memcmp(code, kMsgDFileTransfer, 4) == 0) {
    fileChunkReceived();
  } else if (memcmp(code, kMsgDDragInfo, 4) == 0) {
    dragInfoReceived();
  } else {
    return ClientProxy1_4::parseMessage(code);
  }

  return true;
}

void ClientProxy1_5::fileChunkReceived()
{
  // Reverse direction (client -> server drag) is not wired yet; still drain the
  // payload so the protocol stream stays in sync.
  uint8_t mark = 0;
  std::string data;
  if (!ProtocolUtil::readf(getStream(), kMsgDFileTransfer + 4, &mark, &data)) {
    LOG_WARN("failed to read incoming file chunk");
    return;
  }
  LOG_DEBUG("received (ignored) file chunk mark=%d size=%zu", mark, data.size());
}

void ClientProxy1_5::dragInfoReceived()
{
  uint32_t fileCount = 0;
  std::string data;
  if (!ProtocolUtil::readf(getStream(), kMsgDDragInfo + 4, &fileCount, &data)) {
    LOG_WARN("failed to read incoming drag info");
    return;
  }
  LOG_DEBUG("received (ignored) drag info: %u file(s)", fileCount);
}
