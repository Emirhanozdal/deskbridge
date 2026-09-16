/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2014 - 2016 Symless Ltd
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 *
 * Ported from Input Leap (github.com/input-leap/input-leap,
 * src/lib/platform/MSWindowsDropTarget.cpp) which is identical to the original
 * Synergy/Barrier implementation (Symless Ltd, GPL-2.0). See the header for the
 * list of adaptations; the OLE / CF_HDROP capture logic is preserved.
 */

#include "platform/MSWindowsDropTarget.h"

#include <cassert>

namespace {

// Convert an in-place UTF-16 path (as stored by CF_HDROP) to UTF-8. This tree
// keeps every path in UTF-8, so we use WideCharToMultiByte(CP_UTF8) rather than
// the upstream locale-dependent wcstombs (which mangles non-ASCII names).
std::string wideToUtf8(const wchar_t *w)
{
  if (w == nullptr || w[0] == L'\0') {
    return {};
  }
  const int n = WideCharToMultiByte(CP_UTF8, 0, w, -1, nullptr, 0, nullptr, nullptr);
  if (n <= 1) {
    return {};
  }
  std::string s(static_cast<size_t>(n - 1), '\0');
  WideCharToMultiByte(CP_UTF8, 0, w, -1, s.data(), n, nullptr, nullptr);
  return s;
}

// Pull the (first) dragged file path out of a data object's CF_HDROP block and
// hand it to the drop target instance. Faithful to upstream getDropData().
void getDropData(IDataObject *dataObject)
{
  FORMATETC fmtEtc = {CF_HDROP, nullptr, DVASPECT_CONTENT, -1, TYMED_HGLOBAL};
  STGMEDIUM stgMed;

  if (dataObject->QueryGetData(&fmtEtc) == S_OK) {
    if (dataObject->GetData(&fmtEtc, &stgMed) == S_OK) {
      PVOID data = GlobalLock(stgMed.hGlobal);
      if (data != nullptr) {
        // the global handle holds: DROPFILES header, then a double-null
        // terminated list of file paths. TODO: capture every file, not just
        // the first (matches the upstream single-file limitation).
        auto *wcData = reinterpret_cast<wchar_t *>(reinterpret_cast<LPBYTE>(data) + sizeof(DROPFILES));
        MSWindowsDropTarget::instance().setDraggingFilename(wideToUtf8(wcData));
        GlobalUnlock(stgMed.hGlobal);
      }
      ReleaseStgMedium(&stgMed);
    }
  }
}

} // namespace

MSWindowsDropTarget *MSWindowsDropTarget::s_instance = nullptr;

MSWindowsDropTarget::MSWindowsDropTarget() : m_refCount(1), m_allowDrop(false)
{
  s_instance = this;
}

MSWindowsDropTarget::~MSWindowsDropTarget()
{
  if (s_instance == this) {
    s_instance = nullptr;
  }
}

MSWindowsDropTarget &MSWindowsDropTarget::instance()
{
  assert(s_instance != nullptr);
  return *s_instance;
}

HRESULT
MSWindowsDropTarget::DragEnter(IDataObject *dataObject, DWORD keyState, POINTL point, DWORD *effect)
{
  // check whether the data object carries a file drop; if so, capture its path
  m_allowDrop = queryDataObject(dataObject);
  if (m_allowDrop) {
    getDropData(dataObject);
  }

  *effect = DROPEFFECT_NONE;
  return S_OK;
}

HRESULT
MSWindowsDropTarget::DragOver(DWORD keyState, POINTL point, DWORD *effect)
{
  *effect = DROPEFFECT_NONE;
  return S_OK;
}

HRESULT
MSWindowsDropTarget::DragLeave()
{
  return S_OK;
}

HRESULT
MSWindowsDropTarget::Drop(IDataObject *dataObject, DWORD keyState, POINTL point, DWORD *effect)
{
  *effect = DROPEFFECT_NONE;
  return S_OK;
}

bool MSWindowsDropTarget::queryDataObject(IDataObject *dataObject)
{
  // does it expose CF_HDROP via an HGLOBAL?
  FORMATETC fmtetc = {CF_HDROP, nullptr, DVASPECT_CONTENT, -1, TYMED_HGLOBAL};
  return dataObject->QueryGetData(&fmtetc) == S_OK;
}

void MSWindowsDropTarget::setDraggingFilename(const std::string &filename)
{
  m_dragFilename = filename;
}

std::string MSWindowsDropTarget::getDraggingFilename()
{
  return m_dragFilename;
}

void MSWindowsDropTarget::clearDraggingFilename()
{
  m_dragFilename.clear();
}

HRESULT __stdcall MSWindowsDropTarget::QueryInterface(REFIID iid, void **object)
{
  if (iid == IID_IDropTarget || iid == IID_IUnknown) {
    AddRef();
    *object = this;
    return S_OK;
  }
  *object = nullptr;
  return E_NOINTERFACE;
}

ULONG __stdcall MSWindowsDropTarget::AddRef()
{
  return InterlockedIncrement(&m_refCount);
}

ULONG __stdcall MSWindowsDropTarget::Release()
{
  const LONG count = InterlockedDecrement(&m_refCount);
  if (count == 0) {
    delete this;
    return 0;
  }
  return count;
}
