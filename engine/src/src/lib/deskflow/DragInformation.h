/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2015 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * Restored for DeskBridge cross-screen drag-and-drop.
 *
 * Describes one file participating in a drag. Both ends of a DeskBridge link
 * run the same engine build, so the serialized form only has to be consistent
 * between setupDragInfo() (sender) and parseDragInfo() (receiver); it does not
 * need to match any legacy Barrier/Synergy wire format.
 *
 * Wire form of a drag-info payload (the %s in kMsgDDragInfo):
 *   for each file:  <basename> '\n' <filesize-decimal> '\n'
 */

#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

class DragInformation;
using DragFileList = std::vector<DragInformation>;

class DragInformation
{
public:
  DragInformation() = default;
  DragInformation(std::string filename, size_t filesize) : m_filename(std::move(filename)), m_filesize(filesize)
  {
  }

  const std::string &getFilename() const
  {
    return m_filename;
  }
  void setFilename(std::string name)
  {
    m_filename = std::move(name);
  }

  size_t getFilesize() const
  {
    return m_filesize;
  }
  void setFilesize(size_t size)
  {
    m_filesize = size;
  }

  //! Serialize a list of files into a payload string; returns the file count.
  static uint32_t setupDragInfo(const DragFileList &fileList, std::string &output);

  //! Parse a payload string (expecting fileNum files) back into dragFileList.
  static void parseDragInfo(DragFileList &dragFileList, uint32_t fileNum, const std::string &data);

  //! Lowercase extension (without dot) of filename, or empty.
  static std::string getDragFileExtension(const std::string &filename);

private:
  std::string m_filename;
  size_t m_filesize = 0;
};
