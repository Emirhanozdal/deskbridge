/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2015 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * DeskBridge cross-screen drag-and-drop receiver.
 *
 * Assembles an incoming file transfer by writing chunks STRAIGHT TO DISK as
 * they arrive (a temp file, renamed on completion). Memory use is bounded to a
 * single chunk regardless of file size, so receiving a multi-gigabyte drag does
 * not grow RAM. Reusable on both the client (secondary) and the server
 * (primary) so drags work in either direction.
 */

#pragma once

#include "deskflow/DragInformation.h"

#include <cstdint>
#include <fstream>
#include <string>

class FileReceiver
{
public:
  FileReceiver() = default;
  ~FileReceiver();

  //! Directory incoming files are written into.
  void setDropDirectory(const std::string &dir);

  //! Names + expected sizes announced by the drag-info message, in order.
  void setDragFiles(DragFileList files);

  //! Feed one received chunk (mark is a ChunkType value). Returns the full path
  //! of the file just completed on a DataEnd, or an empty string otherwise.
  std::string onChunk(uint8_t mark, const std::string &data);

  //! Abort any partially-written file (e.g. on disconnect).
  void reset();

private:
  std::string nextFinalName();
  static std::string uniquePath(const std::string &dir, const std::string &name);

  std::string m_dropDir;
  DragFileList m_files;
  size_t m_index = 0;

  std::ofstream m_out;
  std::string m_tempPath;
  std::string m_finalName;
  size_t m_expected = 0;
  size_t m_received = 0;
  bool m_active = false;
};
