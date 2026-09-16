/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2014 - 2016 Symless Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * Ported from Input Leap (github.com/input-leap/input-leap,
 * src/lib/platform/MSWindowsDropTarget.{h,cpp}) which is identical to the
 * original Synergy/Barrier implementation (Symless Ltd, GPL-2.0). Adapted for
 * DeskBridge:
 *   - removed the "inputleap" namespace to match this tree's global class style
 *   - UTF-16 -> UTF-8 conversion via WideCharToMultiByte (this tree keeps all
 *     paths in UTF-8) instead of the locale-dependent wcstombs
 * The OLE / IDropTarget / CF_HDROP capture logic is preserved verbatim.
 */

#pragma once

#include <string>

#define WIN32_LEAN_AND_MEAN
#include <Windows.h>
#include <oleidl.h>

class MSWindowsScreen;

//! IDropTarget used to capture the path of a file dragged off this screen.
/*!
A tiny transparent window (see MSWindowsScreen::createDropWindow) is registered
with this target. When the source-side handoff needs the name of the file the
user is dragging, MSWindowsScreen momentarily teleports that window under the
cursor and forces the in-progress OLE drag to drop onto it; DragEnter then
records the CF_HDROP file path here, which getDraggingFilename() returns.
*/
class MSWindowsDropTarget : public IDropTarget
{
public:
  MSWindowsDropTarget();
  ~MSWindowsDropTarget();

  // IUnknown implementation
  HRESULT __stdcall QueryInterface(REFIID iid, void **object) override;
  ULONG __stdcall AddRef() override;
  ULONG __stdcall Release() override;

  // IDropTarget implementation
  HRESULT __stdcall DragEnter(IDataObject *dataObject, DWORD keyState, POINTL point, DWORD *effect) override;
  HRESULT __stdcall DragOver(DWORD keyState, POINTL point, DWORD *effect) override;
  HRESULT __stdcall DragLeave() override;
  HRESULT __stdcall Drop(IDataObject *dataObject, DWORD keyState, POINTL point, DWORD *effect) override;

  void setDraggingFilename(const std::string &filename);
  std::string getDraggingFilename();
  void clearDraggingFilename();

  static MSWindowsDropTarget &instance();

private:
  bool queryDataObject(IDataObject *dataObject);

  long m_refCount;
  bool m_allowDrop;
  std::string m_dragFilename;

  static MSWindowsDropTarget *s_instance;
};
