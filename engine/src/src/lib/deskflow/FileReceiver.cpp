/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2015 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * DeskBridge cross-screen drag-and-drop receiver.
 */

#include "deskflow/FileReceiver.h"

#include "base/Log.h"
#include "deskflow/ProtocolTypes.h"

#include <filesystem>
#include <system_error>

namespace fs = std::filesystem;

FileReceiver::~FileReceiver()
{
  reset();
}

void FileReceiver::setDropDirectory(const std::string &dir)
{
  m_dropDir = dir;
}

void FileReceiver::setDragFiles(DragFileList files)
{
  m_files = std::move(files);
  m_index = 0;
}

std::string FileReceiver::nextFinalName()
{
  if (m_index < m_files.size()) {
    const std::string &name = m_files[m_index].getFilename();
    if (!name.empty()) {
      return name;
    }
  }
  return "deskbridge-drop.bin";
}

std::string FileReceiver::uniquePath(const std::string &dir, const std::string &name)
{
  const fs::path base(name);
  const std::string stem = base.stem().string();
  const std::string ext = base.extension().string();
  for (int i = 0; i < 10000; ++i) {
    std::string candidate = name;
    if (i > 0) {
      candidate = stem + " (" + std::to_string(i) + ")" + ext;
    }
    const fs::path full = fs::path(dir) / candidate;
    std::error_code ec;
    if (!fs::exists(full, ec)) {
      return full.string();
    }
  }
  return (fs::path(dir) / name).string();
}

std::string FileReceiver::onChunk(uint8_t mark, const std::string &data)
{
  if (mark == ChunkType::DataStart) {
    // start of a new file
    reset();
    if (m_dropDir.empty()) {
      LOG_ERR("drag receive: no drop directory set; dropping transfer");
      return {};
    }
    std::error_code ec;
    fs::create_directories(m_dropDir, ec);

    try {
      m_expected = static_cast<size_t>(std::stoull(data));
    } catch (...) {
      m_expected = 0;
    }
    m_received = 0;
    m_finalName = fs::path(nextFinalName()).filename().string();
    m_tempPath =
        (fs::path(m_dropDir) / (".deskbridge-drop-" + std::to_string(reinterpret_cast<uintptr_t>(this)))).string();
    m_out.open(m_tempPath, std::ios::binary | std::ios::trunc);
    if (!m_out.is_open()) {
      LOG_ERR("drag receive: cannot open temp file in %s", m_dropDir.c_str());
      return {};
    }
    m_active = true;
    LOG_DEBUG("drag receive: starting '%s' expected=%zu", m_finalName.c_str(), m_expected);
    return {};
  }

  if (!m_active) {
    // chunk without a start; ignore
    return {};
  }

  if (mark == ChunkType::DataChunk) {
    m_out.write(data.data(), static_cast<std::streamsize>(data.size()));
    m_received += data.size();
    if (!m_out) {
      LOG_ERR("drag receive: write failed, aborting '%s'", m_finalName.c_str());
      reset();
    }
    return {};
  }

  if (mark == ChunkType::DataEnd) {
    m_out.close();
    const bool ok = m_out.good() || m_out.eof();
    std::error_code ec;
    if (!ok || (m_expected != 0 && m_received != m_expected)) {
      LOG_ERR(
          "drag receive: incomplete '%s' (%zu of %zu bytes), discarding", m_finalName.c_str(), m_received, m_expected
      );
      fs::remove(m_tempPath, ec);
      m_active = false;
      return {};
    }

    const std::string finalPath = uniquePath(m_dropDir, m_finalName);
    fs::rename(m_tempPath, finalPath, ec);
    if (ec) {
      // cross-device or other failure: fall back to copy+remove
      fs::copy_file(m_tempPath, finalPath, fs::copy_options::overwrite_existing, ec);
      fs::remove(m_tempPath, ec);
    }
    m_active = false;
    ++m_index;
    LOG_INFO("drag receive: wrote %zu bytes to %s", m_received, finalPath.c_str());
    return finalPath;
  }

  return {};
}

void FileReceiver::reset()
{
  if (m_out.is_open()) {
    m_out.close();
  }
  if (!m_tempPath.empty()) {
    std::error_code ec;
    std::filesystem::remove(m_tempPath, ec);
  }
  m_tempPath.clear();
  m_finalName.clear();
  m_expected = 0;
  m_received = 0;
  m_active = false;
}
