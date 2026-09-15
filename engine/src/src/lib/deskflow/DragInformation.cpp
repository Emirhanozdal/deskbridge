/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2015 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * Restored for DeskBridge cross-screen drag-and-drop.
 */

#include "deskflow/DragInformation.h"

#include "base/Log.h"

#include <algorithm>
#include <filesystem>

uint32_t DragInformation::setupDragInfo(const DragFileList &fileList, std::string &output)
{
  output.clear();
  uint32_t count = 0;
  for (const auto &info : fileList) {
    // Only the basename crosses the wire; the receiver rebuilds the path under
    // its own drop directory. This also prevents the sender leaking absolute
    // paths and blocks path traversal on the receiving side.
    std::string base = std::filesystem::path(info.getFilename()).filename().string();
    if (base.empty()) {
      continue;
    }
    output.append(base);
    output.push_back('\n');
    output.append(std::to_string(info.getFilesize()));
    output.push_back('\n');
    ++count;
  }
  return count;
}

void DragInformation::parseDragInfo(DragFileList &dragFileList, uint32_t fileNum, const std::string &data)
{
  dragFileList.clear();
  size_t pos = 0;
  for (uint32_t i = 0; i < fileNum; ++i) {
    const size_t nameEnd = data.find('\n', pos);
    if (nameEnd == std::string::npos) {
      break;
    }
    std::string name = data.substr(pos, nameEnd - pos);
    pos = nameEnd + 1;

    const size_t sizeEnd = data.find('\n', pos);
    if (sizeEnd == std::string::npos) {
      break;
    }
    const std::string sizeText = data.substr(pos, sizeEnd - pos);
    pos = sizeEnd + 1;

    size_t filesize = 0;
    try {
      filesize = static_cast<size_t>(std::stoull(sizeText));
    } catch (...) {
      LOG_ERR("drag info: bad file size '%s'", sizeText.c_str());
      continue;
    }

    // Defensive: keep only the basename so a crafted payload cannot escape the
    // drop directory on the receiver.
    name = std::filesystem::path(name).filename().string();
    if (name.empty() || name == "." || name == "..") {
      LOG_ERR("drag info: rejecting suspicious name");
      continue;
    }
    dragFileList.emplace_back(name, filesize);
  }

  LOG_DEBUG("drag info: parsed %zu of %u files", dragFileList.size(), fileNum);
}

std::string DragInformation::getDragFileExtension(const std::string &filename)
{
  std::string ext = std::filesystem::path(filename).extension().string();
  if (!ext.empty() && ext.front() == '.') {
    ext.erase(ext.begin());
  }
  std::transform(ext.begin(), ext.end(), ext.begin(), [](unsigned char c) { return std::tolower(c); });
  return ext;
}
