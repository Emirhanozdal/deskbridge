/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2015 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * Restored for DeskBridge cross-screen drag-and-drop file transfer.
 * Mirrors ClipboardChunk but carries a single mark byte + payload, matching
 * the kMsgDFileTransfer ("DFTR%1i%s") wire format already defined in the
 * protocol. There is no clipboard id or sequence: only one drag file transfer
 * is ever in flight for a given direction.
 */

#pragma once

#include "deskflow/Chunk.h"
#include "deskflow/ProtocolTypes.h"

#include <cstddef>
#include <memory>
#include <semaphore>
#include <string>

// one mark byte + trailing NUL
constexpr static auto s_fileChunkMetaSize = 2;

namespace deskflow {
class IStream;
}

struct FileChunkAssemblyState
{
  size_t expectedSize = 0;
  bool active = false;
};

// Bounded-flow token: sender acquires before queueing a chunk; the queue
// deletes the chunk after it is written, releasing the token so the sender may
// read the next one. Keeps in-flight memory to a few chunks regardless of file
// size.
using FileFlowToken = std::counting_semaphore<>;

class FileChunk : public Chunk
{
public:
  explicit FileChunk(size_t size);
  ~FileChunk() override;

  //! Attach a flow token released when this chunk is destroyed (after sending).
  void setFlowToken(std::shared_ptr<FileFlowToken> token)
  {
    m_flow = std::move(token);
  }

  //! Build a start chunk carrying the total file size (decimal string).
  static FileChunk *start(const std::string &size);
  //! Build a data chunk carrying a slice of the file contents.
  static FileChunk *data(const std::string &data);
  //! Build the terminating chunk.
  static FileChunk *end();

  //! Read one incoming file chunk off the stream and fold it into dataCached.
  static TransferState assemble(
      deskflow::IStream *stream, std::string &dataCached, FileChunkAssemblyState &state, size_t maxDataSize
  );

  //! Write a queued FileChunk out to the stream.
  static void send(deskflow::IStream *stream, void *data);

  static size_t getExpectedSize(const FileChunkAssemblyState &state)
  {
    return state.expectedSize;
  }

private:
  std::shared_ptr<FileFlowToken> m_flow;
};
