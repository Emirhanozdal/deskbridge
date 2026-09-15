/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2015 - 2016 Synergy App Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * Restored for DeskBridge cross-screen drag-and-drop.
 */

#pragma once

#include <string>

class DropHelper
{
public:
  //! Write received drag payload to <dir>/<basename>, returning the full path
  //! actually written (empty on failure). The name is sanitized to its
  //! basename so it can never escape dir.
  static std::string writeToDir(const std::string &dir, const std::string &basename, const std::string &data);
};
