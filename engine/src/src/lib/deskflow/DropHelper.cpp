/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2015 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * Restored for DeskBridge cross-screen drag-and-drop.
 */

#include "deskflow/DropHelper.h"

#include "base/Log.h"

#include <filesystem>
#include <fstream>

std::string DropHelper::writeToDir(const std::string &dir, const std::string &basename, const std::string &data)
{
  if (dir.empty()) {
    LOG_ERR("drop: no drop directory configured");
    return {};
  }

  // Sanitize: keep only the basename so the target can never escape dir.
  std::string safe = std::filesystem::path(basename).filename().string();
  if (safe.empty() || safe == "." || safe == "..") {
    LOG_ERR("drop: refusing suspicious filename");
    return {};
  }

  std::error_code ec;
  std::filesystem::create_directories(dir, ec);

  const std::filesystem::path target = std::filesystem::path(dir) / safe;
  std::ofstream out(target, std::ios::binary | std::ios::trunc);
  if (!out.is_open()) {
    LOG_ERR("drop: cannot open target for writing: %s", target.string().c_str());
    return {};
  }
  out.write(data.data(), static_cast<std::streamsize>(data.size()));
  out.close();
  if (!out) {
    LOG_ERR("drop: write failed: %s", target.string().c_str());
    return {};
  }

  LOG_INFO("drop: wrote %zu bytes to %s", data.size(), target.string().c_str());
  return target.string();
}
