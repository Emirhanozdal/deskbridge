/*
 * Deskflow -- mouse and keyboard sharing utility
 * SPDX-FileCopyrightText: (C) 2025 - 2026 Deskflow Developers
 * SPDX-FileCopyrightText: (C) 2012 - 2016 Synergy App Ltd
 * SPDX-FileCopyrightText: (C) 2002 Chris Schoeneman
 * SPDX-License-Identifier: GPL-2.0-only WITH LicenseRef-OpenSSL-Exception
 */

#include "platform/MSWindowsScreen.h"

#include "arch/Arch.h"
#include "arch/win32/ArchMiscWindows.h"
#include "arch/win32/XArchWindows.h"
#include "base/IEventQueue.h"
#include "base/Log.h"
#include "base/TMethodJob.h"
#include "client/Client.h"
#include "common/Constants.h"
#include "common/Settings.h"
#include "deskflow/App.h"
#include "deskflow/ClientApp.h"
#include "deskflow/Clipboard.h"
#include "deskflow/KeyMap.h"
#include "deskflow/ScreenException.h"
#include "platform/MSWindowsClipboard.h"
#include "platform/MSWindowsDesks.h"
#include "platform/MSWindowsDropTarget.h"
#include "platform/MSWindowsEventQueueBuffer.h"
#include "platform/MSWindowsKeyState.h"
#include "platform/MSWindowsScreenSaver.h"

#include <Shlobj.h>
#include <algorithm>
#include <comutil.h>
#include <string.h>

// suppress warning about GetVersionEx, which is used indirectly in this
// compilation unit.
#pragma warning(disable : 4996)

//
// add backwards compatible multihead support (and suppress bogus warning).
// this isn't supported on MinGW yet AFAICT.
//
#if defined(_MSC_VER)
#pragma warning(push)
#pragma warning(disable : 4706) // assignment within conditional
#define COMPILE_MULTIMON_STUBS
#include <multimon.h>
#pragma warning(pop)
#endif

// X button stuff
#if !defined(WM_XBUTTONDOWN)
#define WM_XBUTTONDOWN 0x020B
#define WM_XBUTTONUP 0x020C
#define WM_XBUTTONDBLCLK 0x020D
#define WM_NCXBUTTONDOWN 0x00AB
#define WM_NCXBUTTONUP 0x00AC
#define WM_NCXBUTTONDBLCLK 0x00AD
#define MOUSEEVENTF_XDOWN 0x0080
#define MOUSEEVENTF_XUP 0x0100
#define XBUTTON1 0x0001
#define XBUTTON2 0x0002
#endif
#if !defined(VK_XBUTTON1)
#define VK_XBUTTON1 0x05
#define VK_XBUTTON2 0x06
#endif

// WM_POWERBROADCAST stuff
#if !defined(PBT_APMRESUMEAUTOMATIC)
#define PBT_APMRESUMEAUTOMATIC 0x0012
#endif

//
// DeskBridge cross-screen drag-and-drop (OLE) helpers
//
// Source side (this machine is where the user starts dragging a file): the file
// name is captured with the proven Synergy/Input Leap technique implemented in
// MSWindowsDropTarget + extractDraggingFilename() below (a hidden drop window is
// teleported under the cursor and the drag is forced to drop onto it).
//
// Target side (this machine received the file bytes): the synthetic OLE drag
// below (createHDrop / FileDataObject / FileDropSource / runDragThread) drops
// the received file(s) into whatever window the user releases over. NOTE: no
// upstream GPL project ships a Windows receive-as-OLE-drag implementation
// (Synergy/Barrier/Input Leap all leave fakeDraggingFiles() empty and rely on
// the file simply being written into the drop-target folder). This synthetic
// drag is therefore DeskBridge-original and has NOT been validated on real
// Windows hardware; it must be exercised on a Windows box before shipping.
//
namespace {

// Hard upper bound on how long a synthetic drag may run before it is forcibly
// cancelled, so a lost pointer-release can never leave DoDragDrop() (and the
// mouse button) stuck. Mirrors XWindowsScreen's xdndCheckDeadline safety net,
// but generous because a real user is dragging across a physical link.
constexpr double kDragTimeoutSeconds = 30.0;

// Build a CF_HDROP HGLOBAL from a list of absolute local file paths.
// Returns nullptr if no usable path was supplied. Ownership passes to the
// data object that serves it.
HGLOBAL createHDrop(const DragFileList &files)
{
  std::vector<std::wstring> wide;
  size_t chars = 0;
  for (const auto &f : files) {
    const std::string &p = f.getFilename();
    if (p.empty()) {
      continue;
    }
    const int n = MultiByteToWideChar(CP_UTF8, 0, p.c_str(), -1, nullptr, 0);
    if (n <= 1) {
      continue;
    }
    std::wstring w(static_cast<size_t>(n - 1), L'\0');
    MultiByteToWideChar(CP_UTF8, 0, p.c_str(), -1, w.data(), n);
    chars += w.size() + 1; // path + its null terminator
    wide.push_back(std::move(w));
  }
  if (wide.empty()) {
    return nullptr;
  }
  chars += 1; // final extra null: the list is double-null terminated

  const SIZE_T bytes = sizeof(DROPFILES) + chars * sizeof(wchar_t);
  HGLOBAL handle = GlobalAlloc(GHND, bytes);
  if (handle == nullptr) {
    return nullptr;
  }

  auto *drop = static_cast<DROPFILES *>(GlobalLock(handle));
  drop->pFiles = sizeof(DROPFILES);
  drop->pt.x = 0;
  drop->pt.y = 0;
  drop->fNC = FALSE;
  drop->fWide = TRUE; // paths are UTF-16
  auto *dst = reinterpret_cast<wchar_t *>(reinterpret_cast<BYTE *>(drop) + sizeof(DROPFILES));
  for (const auto &w : wide) {
    memcpy(dst, w.c_str(), (w.size() + 1) * sizeof(wchar_t));
    dst += w.size() + 1;
  }
  *dst = L'\0'; // double-null terminate
  GlobalUnlock(handle);
  return handle;
}

// Minimal IDataObject exposing exactly one CF_HDROP (the received files).
class FileDataObject : public IDataObject
{
public:
  explicit FileDataObject(HGLOBAL hdrop) : m_hdrop(hdrop)
  {
  }
  ~FileDataObject()
  {
    if (m_hdrop != nullptr) {
      GlobalFree(m_hdrop);
    }
  }

  // IUnknown
  HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void **ppv) override
  {
    if (ppv == nullptr) {
      return E_INVALIDARG;
    }
    if (riid == IID_IUnknown || riid == IID_IDataObject) {
      *ppv = static_cast<IDataObject *>(this);
      AddRef();
      return S_OK;
    }
    *ppv = nullptr;
    return E_NOINTERFACE;
  }
  ULONG STDMETHODCALLTYPE AddRef() override
  {
    return ++m_ref;
  }
  ULONG STDMETHODCALLTYPE Release() override
  {
    const ULONG r = --m_ref;
    if (r == 0) {
      delete this;
    }
    return r;
  }

  // IDataObject
  HRESULT STDMETHODCALLTYPE GetData(FORMATETC *fmt, STGMEDIUM *med) override
  {
    if (fmt == nullptr || med == nullptr) {
      return E_INVALIDARG;
    }
    if (fmt->cfFormat != CF_HDROP || (fmt->tymed & TYMED_HGLOBAL) == 0 || m_hdrop == nullptr) {
      return DV_E_FORMATETC;
    }
    // hand the caller its own copy so ownership is clean
    const SIZE_T size = GlobalSize(m_hdrop);
    HGLOBAL copy = GlobalAlloc(GHND, size);
    if (copy == nullptr) {
      return E_OUTOFMEMORY;
    }
    void *src = GlobalLock(m_hdrop);
    void *dst = GlobalLock(copy);
    memcpy(dst, src, size);
    GlobalUnlock(copy);
    GlobalUnlock(m_hdrop);
    med->tymed = TYMED_HGLOBAL;
    med->hGlobal = copy;
    med->pUnkForRelease = nullptr;
    return S_OK;
  }
  HRESULT STDMETHODCALLTYPE GetDataHere(FORMATETC *, STGMEDIUM *) override
  {
    return E_NOTIMPL;
  }
  HRESULT STDMETHODCALLTYPE QueryGetData(FORMATETC *fmt) override
  {
    if (fmt == nullptr) {
      return E_INVALIDARG;
    }
    return (fmt->cfFormat == CF_HDROP && (fmt->tymed & TYMED_HGLOBAL) != 0) ? S_OK : DV_E_FORMATETC;
  }
  HRESULT STDMETHODCALLTYPE GetCanonicalFormatEtc(FORMATETC *, FORMATETC *out) override
  {
    if (out != nullptr) {
      out->ptd = nullptr;
    }
    return E_NOTIMPL;
  }
  HRESULT STDMETHODCALLTYPE SetData(FORMATETC *, STGMEDIUM *, BOOL) override
  {
    return E_NOTIMPL;
  }
  HRESULT STDMETHODCALLTYPE EnumFormatEtc(DWORD dir, IEnumFORMATETC **out) override
  {
    if (dir != DATADIR_GET || out == nullptr) {
      return E_NOTIMPL;
    }
    FORMATETC fmt = {CF_HDROP, nullptr, DVASPECT_CONTENT, -1, TYMED_HGLOBAL};
    return SHCreateStdEnumFormatEtc(1, &fmt, out);
  }
  HRESULT STDMETHODCALLTYPE DAdvise(FORMATETC *, DWORD, IAdviseSink *, DWORD *) override
  {
    return OLE_E_ADVISENOTSUPPORTED;
  }
  HRESULT STDMETHODCALLTYPE DUnadvise(DWORD) override
  {
    return OLE_E_ADVISENOTSUPPORTED;
  }
  HRESULT STDMETHODCALLTYPE EnumDAdvise(IEnumSTATDATA **) override
  {
    return OLE_E_ADVISENOTSUPPORTED;
  }

private:
  std::atomic<ULONG> m_ref{1};
  HGLOBAL m_hdrop;
};

// IDropSource that follows the relayed pointer/button: the drag ends with a
// drop when the (relayed) left button is released, and is cancelled on Escape,
// on an external cancel request, or after kDragTimeoutSeconds.
class FileDropSource : public IDropSource
{
public:
  explicit FileDropSource(std::atomic<bool> *cancel) : m_cancel(cancel), m_start(ARCH->time())
  {
  }

  HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void **ppv) override
  {
    if (ppv == nullptr) {
      return E_INVALIDARG;
    }
    if (riid == IID_IUnknown || riid == IID_IDropSource) {
      *ppv = static_cast<IDropSource *>(this);
      AddRef();
      return S_OK;
    }
    *ppv = nullptr;
    return E_NOINTERFACE;
  }
  ULONG STDMETHODCALLTYPE AddRef() override
  {
    return ++m_ref;
  }
  ULONG STDMETHODCALLTYPE Release() override
  {
    const ULONG r = --m_ref;
    if (r == 0) {
      delete this;
    }
    return r;
  }

  HRESULT STDMETHODCALLTYPE QueryContinueDrag(BOOL escapePressed, DWORD keyState) override
  {
    if (escapePressed || (m_cancel != nullptr && m_cancel->load()) ||
        (ARCH->time() - m_start) > kDragTimeoutSeconds) {
      return DRAGDROP_S_CANCEL;
    }
    // the relayed left-button release completes the drop
    if ((keyState & MK_LBUTTON) == 0) {
      return DRAGDROP_S_DROP;
    }
    return S_OK;
  }
  HRESULT STDMETHODCALLTYPE GiveFeedback(DWORD) override
  {
    return DRAGDROP_S_USEDEFAULTCURSORS;
  }

private:
  std::atomic<ULONG> m_ref{1};
  std::atomic<bool> *m_cancel;
  double m_start;
};

} // namespace

//
// MSWindowsScreen
//

HINSTANCE MSWindowsScreen::s_windowInstance = nullptr;
MSWindowsScreen *MSWindowsScreen::s_screen = nullptr;

MSWindowsScreen::MSWindowsScreen(bool isPrimary, bool useHooks, IEventQueue *events, bool enableLangSync)
    : PlatformScreen(events),
      m_isPrimary(isPrimary),
      m_useHooks(useHooks),
      m_isOnScreen(m_isPrimary),
      m_hasMouse(GetSystemMetrics(SM_MOUSEPRESENT) != 0),
      m_events(events)
{
  LOG_DEBUG("settting up %s screen", m_isPrimary ? "primary" : "secondary");

  assert(s_windowInstance != nullptr);
  assert(s_screen == nullptr);

  s_screen = this;
  try {
    if (m_isPrimary && m_useHooks) {
      m_hook.loadLibrary();
    }

    m_screensaver = new MSWindowsScreenSaver();
    m_desks = new MSWindowsDesks(
        m_isPrimary, m_useHooks, m_screensaver, m_events,
        new TMethodJob<MSWindowsScreen>(this, &MSWindowsScreen::updateKeysCB)
    );
    m_keyState = new MSWindowsKeyState(
        m_desks, getEventTarget(), m_events, AppUtil::instance().getKeyboardLayoutList(), enableLangSync
    );

    updateScreenShape();
    m_class = createWindowClass();
    m_window = createWindow(m_class, LPCWSTR(kAppName));
    setupMouseKeys();
    LOG_DEBUG("screen shape: %d,%d %dx%d %s", m_x, m_y, m_w, m_h, m_multimon ? "(multi-monitor)" : "");
    LOG_DEBUG("window is 0x%08x", m_window);

    if (Settings::value(Settings::Core::PreventSleep).toBool()) {
      m_powerManager.disableSleep();
    }

    OleInitialize(0);

    // Source-side drag capture (proven Synergy/Input Leap technique): a small
    // transparent, accept-files window registered as an OLE drop target. When a
    // file drag is handed to the peer, extractDraggingFilename() teleports this
    // window under the cursor and forces the drag to drop onto it so the dragged
    // path can be read from CF_HDROP.
    m_dropWindow = createDropWindow(m_class, L"DeskBridgeDropWindow");
    m_dropTarget = new MSWindowsDropTarget();
    if (const HRESULT hr = RegisterDragDrop(m_dropWindow, m_dropTarget); FAILED(hr)) {
      LOG_WARN("drag: RegisterDragDrop failed (0x%08lx); source-side file drag disabled", hr);
    }
  } catch (...) {
    delete m_keyState;
    delete m_desks;
    delete m_screensaver;
    destroyWindow(m_window);
    destroyClass(m_class);
    s_screen = nullptr;
    throw;
  }

  // install event handlers
  m_events->addHandler(EventTypes::System, m_events->getSystemTarget(), [this](const auto &e) {
    handleSystemEvent(e);
  });

  // install the platform event queue
  m_events->adoptBuffer(new MSWindowsEventQueueBuffer(m_events));
}

MSWindowsScreen::~MSWindowsScreen()
{
  assert(s_screen != nullptr);

  disable();
  m_events->adoptBuffer(nullptr);
  m_events->removeHandler(EventTypes::System, m_events->getSystemTarget());
  delete m_keyState;
  delete m_desks;
  delete m_screensaver;

  // tear down the source-side OLE drop target/window
  if (m_dropWindow != nullptr) {
    RevokeDragDrop(m_dropWindow);
  }
  if (m_dropTarget != nullptr) {
    m_dropTarget->Release();
    m_dropTarget = nullptr;
  }
  destroyWindow(m_dropWindow);
  m_dropWindow = nullptr;

  destroyWindow(m_window);
  destroyClass(m_class);

  OleUninitialize();

  s_screen = nullptr;
}

void MSWindowsScreen::init(HINSTANCE windowInstance)
{
  assert(s_windowInstance == nullptr);
  assert(windowInstance != nullptr);

  s_windowInstance = windowInstance;
}

HINSTANCE
MSWindowsScreen::getWindowInstance()
{
  return s_windowInstance;
}

void MSWindowsScreen::enable()
{
  LOG_DEBUG("enabling %s screen", m_isPrimary ? "primary" : "secondary");
  m_isEnabled = true;

  assert(m_isOnScreen == m_isPrimary);

  // we need to poll some things to fix them
  m_fixTimer = m_events->newTimer(1.0, nullptr);
  m_events->addHandler(EventTypes::Timer, m_fixTimer, [this](const auto &) { handleFixes(); });

  // install our clipboard snooper
  if (!AddClipboardFormatListener(m_window)) {
    LOG_WARN("failed to add the clipboard format listener: %d", GetLastError());
  }

  // track the active desk and (re)install the hooks
  m_desks->enable();

  if (m_isPrimary) {
    // set jump zones
    m_hook.setZone(m_x, m_y, m_w, m_h, getJumpZoneSize());

    // watch jump zones
    m_hook.setMode(kHOOK_WATCH_JUMP_ZONE);
  }
}

void MSWindowsScreen::disable()
{
  LOG_DEBUG("disabling %s screen", m_isPrimary ? "primary" : "secondary");
  m_isEnabled = false;

  // stop tracking the active desk
  m_desks->disable();

  if (m_isPrimary) {
    // disable hooks
    m_hook.setMode(kHOOK_DISABLE);

    // enable special key sequences on win95 family
    enableSpecialKeys(true);
  }

  // tear down any in-progress synthetic (target-side) drag
  stopDragThread();

  // tell key state
  m_keyState->disable();

  // stop snooping the clipboard
  if (!RemoveClipboardFormatListener(m_window)) {
    LOG_WARN("failed to remove the clipboard format listener: %d", GetLastError());
  }

  // uninstall fix timer
  if (m_fixTimer != nullptr) {
    m_events->removeHandler(EventTypes::Timer, m_fixTimer);
    m_events->deleteTimer(m_fixTimer);
    m_fixTimer = nullptr;
  }

  m_isOnScreen = m_isPrimary;
}

void MSWindowsScreen::enter()
{
  m_desks->enter();
  if (m_isPrimary) {
    // enable special key sequences on win95 family
    enableSpecialKeys(true);

    // watch jump zones
    m_hook.setMode(kHOOK_WATCH_JUMP_ZONE);

    // all messages prior to now are invalid
    nextMark();

    m_primaryKeyDownList.clear();
  } else {
    // Entering a secondary screen. Ensure that no screensaver is active
    // and that the screen is not in powersave mode.
    ArchMiscWindows::wakeupDisplay();

    if (m_screensaver != nullptr && m_screensaverActive) {
      m_screensaver->deactivate();
      m_screensaverActive = 0;
    }
  }

  // now on screen
  m_isOnScreen = true;
  setupMouseKeys();
}

bool MSWindowsScreen::canLeave()
{
  POINT pos;
  if (!getThisCursorPos(&pos)) {
    // prevent screen leave when cursor position is not available; if unable to get cursor position,
    // screen will become inaccessible if the cursor leaves the screen.
    LOG_DEBUG("unable to leave screen, cursor position not available");
    return false;
  }

  return true;
}

void MSWindowsScreen::leave()
{
  // get keyboard layout of foreground window.  we'll use this
  // keyboard layout for translating keys sent to clients.
  m_keyLayout = AppUtilWindows::instance().getCurrentKeyboardLayout();

  // tell the key mapper about the keyboard layout
  m_keyState->setKeyLayout(m_keyLayout);

  // tell desk that we're leaving and tell it the keyboard layout
  m_desks->leave(m_keyLayout);

  if (m_isPrimary) {
    LOG_VERBOSE("centering cursor on leave: %+d, %+d", m_xCenter, m_yCenter);
    warpCursor(m_xCenter, m_yCenter);

    // disable special key sequences on win95 family
    enableSpecialKeys(false);

    // all messages prior to now are invalid
    nextMark();

    // remember the modifier state.  this is the modifier state
    // reflected in the internal keyboard state.
    m_keyState->saveModifiers();

    m_hook.setMode(kHOOK_RELAY_EVENTS);

    m_primaryKeyDownList.clear();
    for (KeyButton i = 0; i < IKeyState::s_numButtons; ++i) {
      if (m_keyState->isKeyDown(i)) {
        m_primaryKeyDownList.push_back(i);
        LOG_VERBOSE("key button %d is down before leaving to another screen", i);
      }
    }
  }

  // now off screen
  m_isOnScreen = false;
}

bool MSWindowsScreen::setClipboard(ClipboardID, const IClipboard *src)
{
  MSWindowsClipboard dst(m_window);
  if (src != nullptr) {
    // save clipboard data
    return Clipboard::copy(&dst, src);
  } else {
    // assert clipboard ownership
    if (!dst.open(0)) {
      return false;
    }
    dst.empty();
    dst.close();
    return true;
  }
}

void MSWindowsScreen::checkClipboards()
{
  // if we think we own the clipboard but we don't then somebody
  // grabbed the clipboard on this screen without us knowing.
  // tell the server that this screen grabbed the clipboard.
  //
  // this works around bugs in the clipboard viewer chain.
  // sometimes NT will simply never send WM_DRAWCLIPBOARD
  // messages for no apparent reason and rebooting fixes the
  // problem.  since we don't want a broken clipboard until the
  // next reboot we do this double check.  clipboard ownership
  // won't be reflected on other screens until we leave but at
  // least the clipboard itself will work.
  if (m_ownClipboard && !MSWindowsClipboard::isOwnedByDeskflow()) {
    LOG_DEBUG("clipboard changed: lost ownership and no notification received");
    m_ownClipboard = false;
    sendClipboardEvent(EventTypes::ClipboardGrabbed, kClipboardClipboard);
    sendClipboardEvent(EventTypes::ClipboardGrabbed, kClipboardSelection);
  }
}

void MSWindowsScreen::openScreensaver(bool notify)
{
  assert(m_screensaver != nullptr);

  m_screensaverNotify = notify;
  if (m_screensaverNotify) {
    m_desks->installScreensaverHooks(true);
  } else if (m_screensaver) {
    m_screensaver->disable();
  }
}

void MSWindowsScreen::closeScreensaver()
{
  if (m_screensaver != nullptr) {
    if (m_screensaverNotify) {
      m_desks->installScreensaverHooks(false);
    } else {
      m_screensaver->enable();
    }
  }
  m_screensaverNotify = false;
}

void MSWindowsScreen::screensaver(bool activate)
{
  assert(m_screensaver != nullptr);
  if (m_screensaver == nullptr)
    return;

  if (activate) {
    m_screensaver->activate();
  } else {
    m_screensaver->deactivate();
  }
}

void MSWindowsScreen::resetOptions()
{
  m_desks->resetOptions();
}

void MSWindowsScreen::setOptions(const OptionsList &options)
{
  m_desks->setOptions(options);
}

void MSWindowsScreen::setSequenceNumber(uint32_t seqNum)
{
  m_sequenceNumber = seqNum;
}

bool MSWindowsScreen::isPrimary() const
{
  return m_isPrimary;
}

void *MSWindowsScreen::getEventTarget() const
{
  return const_cast<MSWindowsScreen *>(this);
}

bool MSWindowsScreen::getClipboard(ClipboardID, IClipboard *dst) const
{
  MSWindowsClipboard src(m_window);
  Clipboard::copy(dst, &src);
  return true;
}

void MSWindowsScreen::getShape(int32_t &x, int32_t &y, int32_t &w, int32_t &h) const
{
  assert(m_class != 0);

  x = m_x;
  y = m_y;
  w = m_w;
  h = m_h;
}

void MSWindowsScreen::getCursorPos(int32_t &x, int32_t &y) const
{
  m_desks->getCursorPos(x, y);
}

/*
 * getThisCursorPos and setThisCursorPos will attempt to negotiate with the
 * system to try get the and set the mouse position, however on the logon screen
 * due to hooks this process has it may unable to work around the problem.
 * Although these functions did not fix the issue at hand (#5294) its worth
 * keeping them here anyway.
 */
bool MSWindowsScreen::getThisCursorPos(LPPOINT pos)
{
  auto result = GetCursorPos(pos);
  if (!result) {
    LOG_DEBUG("could not get cursor pos, error: %s", windowsErrorToString(GetLastError()).c_str());

    LOG_DEBUG("retrying get cursor pos");
    result = GetCursorPos(pos);
    if (!result) {
      LOG_DEBUG("could not get cursor pos, error: %s", windowsErrorToString(GetLastError()).c_str());

      updateDesktopThread();
    }
  }

  return result;
}

bool MSWindowsScreen::setThisCursorPos(int x, int y)
{
  auto result = SetCursorPos(x, y);
  if (!result) {
    LOG_DEBUG("could not set cursor pos, error: %s", windowsErrorToString(GetLastError()).c_str());

    LOG_DEBUG("retrying to set cursor pos");
    result = SetCursorPos(x, y);
    if (!result) {
      LOG_DEBUG("could not set cursor pos, error: %s", windowsErrorToString(GetLastError()).c_str());

      updateDesktopThread();
    }
  }

  return result;
}

void MSWindowsScreen::updateDesktopThread()
{
  LOG_DEBUG("updating desktop thread");

  HDESK hDesk = OpenInputDesktop(0, true, GENERIC_ALL);
  if (hDesk == nullptr) {
    LOG_DEBUG("could not open input desktop, error: %s", windowsErrorToString(GetLastError()).c_str());
    return;
  }

  if (!SetThreadDesktop(hDesk)) {
    LOG_DEBUG("could not set thread desktop, error: %s", windowsErrorToString(GetLastError()).c_str());
  }

  if (!CloseDesktop(hDesk)) {
    LOG_DEBUG("could not close desktop, error: %s", windowsErrorToString(GetLastError()).c_str());
  }
}

void MSWindowsScreen::reconfigure(uint32_t activeSides)
{
  assert(m_isPrimary);
  const static auto sidesText = sidesMaskToString(activeSides);
  LOG_DEBUG("active sides: %s (0x%02x)", sidesText.c_str(), activeSides);
  m_hook.setSides(activeSides);
}

uint32_t MSWindowsScreen::activeSides()
{
  return m_hook.getSides();
}

void MSWindowsScreen::warpCursor(int32_t x, int32_t y)
{
  // warp mouse
  warpCursorNoFlush(x, y);

  // remove all input events before and including warp
  MSG msg;
  while (PeekMessage(&msg, nullptr, DESKFLOW_MSG_INPUT_FIRST, DESKFLOW_MSG_INPUT_LAST, PM_REMOVE)) {
    // do nothing
  }

  // save position to compute delta of next motion
  saveMousePosition(x, y);
}

void MSWindowsScreen::saveMousePosition(int32_t x, int32_t y)
{
  m_xCursor = x;
  m_yCursor = y;

  LOG_VERBOSE("saved mouse position for next delta: %+d,%+d", x, y);
}

uint32_t MSWindowsScreen::registerHotKey(KeyID key, KeyModifierMask mask)
{
  // only allow certain modifiers
  if ((mask & ~(KeyModifierShift | KeyModifierControl | KeyModifierAlt | KeyModifierSuper)) != 0) {
    // this should be a warning, but this can confuse users,
    // as this warning happens almost always.
    LOG_DEBUG("could not map hotkey id=%04x mask=%04x", key, mask);
    return 0;
  }

  // fail if no keys
  if (key == kKeyNone && mask == 0) {
    return 0;
  }

  // convert to win32
  UINT modifiers = 0;
  if ((mask & KeyModifierShift) != 0) {
    modifiers |= MOD_SHIFT;
  }
  if ((mask & KeyModifierControl) != 0) {
    modifiers |= MOD_CONTROL;
  }
  if ((mask & KeyModifierAlt) != 0) {
    modifiers |= MOD_ALT;
  }
  if ((mask & KeyModifierSuper) != 0) {
    modifiers |= MOD_WIN;
  }
  UINT vk = m_keyState->mapKeyToVirtualKey(key);
  if (key != kKeyNone && vk == 0) {
    // can't map key
    // this should be a warning, but this can confuse users,
    // as this warning happens almost always.
    LOG_DEBUG("could not map hotkey id=%04x mask=%04x", key, mask);
    return 0;
  }

  // choose hotkey id
  uint32_t id;
  if (!m_oldHotKeyIDs.empty()) {
    id = m_oldHotKeyIDs.back();
    m_oldHotKeyIDs.pop_back();
  } else {
    // id = m_hotKeys.size() + 1;
    id = (uint32_t)m_hotKeys.size() + 1;
  }

  // if this hot key has modifiers only then we'll handle it specially
  bool err;
  if (key == kKeyNone) {
    // check if already registered
    err = (m_hotKeyToIDMap.count(HotKeyItem(vk, modifiers)) > 0);
  } else {
    // register with OS
    err = (RegisterHotKey(nullptr, id, modifiers, vk) == 0);
  }

  if (!err) {
    m_hotKeys.try_emplace(id, HotKeyItem(vk, modifiers));
    m_hotKeyToIDMap[HotKeyItem(vk, modifiers)] = id;
  } else {
    m_oldHotKeyIDs.push_back(id);
    m_hotKeys.erase(id);
    LOG_WARN(
        "failed to register hotkey %s (id=%04x mask=%04x)", deskflow::KeyMap::formatKey(key, mask).c_str(), key, mask
    );

    return 0;
  }

  LOG_DEBUG(
      "registered hotkey %s (id=%04x mask=%04x) as id=%d", deskflow::KeyMap::formatKey(key, mask).c_str(), key, mask, id
  );
  return id;
}

void MSWindowsScreen::unregisterHotKey(uint32_t id)
{
  // look up hotkey
  HotKeyMap::iterator i = m_hotKeys.find(id);
  if (i == m_hotKeys.end()) {
    return;
  }

  // unregister with OS
  bool err;
  if (i->second.getVirtualKey() != 0) {
    err = !UnregisterHotKey(nullptr, id);
  } else {
    err = false;
  }
  if (err) {
    LOG_WARN("failed to unregister hotkey id=%d", id);
  } else {
    LOG_DEBUG("unregistered hotkey id=%d", id);
  }

  // discard hot key from map and record old id for reuse
  m_hotKeyToIDMap.erase(i->second);
  m_hotKeys.erase(i);
  m_oldHotKeyIDs.push_back(id);
}

void MSWindowsScreen::fakeInputBegin()
{
  assert(m_isPrimary);

  if (!m_isOnScreen) {
    m_keyState->useSavedModifiers(true);
  }
  m_desks->fakeInputBegin();
}

void MSWindowsScreen::fakeInputEnd()
{
  assert(m_isPrimary);

  m_desks->fakeInputEnd();
  if (!m_isOnScreen) {
    m_keyState->useSavedModifiers(false);
  }
}

int32_t MSWindowsScreen::getJumpZoneSize() const
{
  return 1;
}

bool MSWindowsScreen::isAnyMouseButtonDown(uint32_t &buttonID) const
{
  static const char *buttonToName[] = {"<invalid>",    "Left Button", "Middle Button",
                                       "Right Button", "X Button 1",  "X Button 2"};

  for (uint32_t i = 1; i < sizeof(m_buttons) / sizeof(m_buttons[0]); ++i) {
    if (m_buttons[i]) {
      buttonID = i;
      LOG_DEBUG("locked by \"%s\"", buttonToName[i]);
      return true;
    }
  }

  return false;
}

void MSWindowsScreen::getCursorCenter(int32_t &x, int32_t &y) const
{
  x = m_xCenter;
  y = m_yCenter;
}

void MSWindowsScreen::fakeMouseButton(ButtonID id, bool press)
{
  m_desks->fakeMouseButton(id, press);

  if (id == kButtonLeft) {
    m_buttons[kButtonLeft] = press;
  }
}

void MSWindowsScreen::fakeMouseMove(int32_t x, int32_t y)
{
  m_desks->fakeMouseMove(x, y);
}

void MSWindowsScreen::fakeMouseRelativeMove(int32_t dx, int32_t dy) const
{
  m_desks->fakeMouseRelativeMove(dx, dy);
}

void MSWindowsScreen::fakeMouseWheel(ScrollDelta delta) const
{
  delta = applyScrollModifier(delta);
  m_desks->fakeMouseWheel(delta.x, delta.y);
}

void MSWindowsScreen::updateKeys()
{
  m_desks->updateKeys();
}

void MSWindowsScreen::fakeKeyDown(KeyID id, KeyModifierMask mask, KeyButton button, const std::string &lang)
{
  PlatformScreen::fakeKeyDown(id, mask, button, lang);
  updateMouseKeys();
}

bool MSWindowsScreen::fakeKeyRepeat(
    KeyID id, KeyModifierMask mask, int32_t count, KeyButton button, const std::string &lang
)
{
  bool result = PlatformScreen::fakeKeyRepeat(id, mask, count, button, lang);
  updateMouseKeys();
  return result;
}

bool MSWindowsScreen::fakeKeyUp(KeyButton button)
{
  bool result = PlatformScreen::fakeKeyUp(button);
  updateMouseKeys();
  return result;
}

void MSWindowsScreen::fakeAllKeysUp()
{
  PlatformScreen::fakeAllKeysUp();
  updateMouseKeys();
}

HCURSOR
MSWindowsScreen::createBlankCursor() const
{
  // create a transparent cursor
  int cw = GetSystemMetrics(SM_CXCURSOR);
  int ch = GetSystemMetrics(SM_CYCURSOR);

  uint8_t *cursorAND = new uint8_t[ch * ((cw + 31) >> 2)];
  uint8_t *cursorXOR = new uint8_t[ch * ((cw + 31) >> 2)];
  memset(cursorAND, 0xff, ch * ((cw + 31) >> 2));
  memset(cursorXOR, 0x00, ch * ((cw + 31) >> 2));
  HCURSOR c = CreateCursor(s_windowInstance, 0, 0, cw, ch, cursorAND, cursorXOR);
  delete[] cursorXOR;
  delete[] cursorAND;
  return c;
}

void MSWindowsScreen::destroyCursor(HCURSOR cursor) const
{
  if (cursor != nullptr) {
    DestroyCursor(cursor);
  }
}

ATOM MSWindowsScreen::createWindowClass() const
{
  WNDCLASSEX classInfo;
  classInfo.cbSize = sizeof(classInfo);
  classInfo.style = CS_DBLCLKS | CS_NOCLOSE;
  classInfo.lpfnWndProc = &MSWindowsScreen::wndProc;
  classInfo.cbClsExtra = 0;
  classInfo.cbWndExtra = 0;
  classInfo.hInstance = s_windowInstance;
  classInfo.hIcon = nullptr;
  classInfo.hCursor = nullptr;
  classInfo.hbrBackground = nullptr;
  classInfo.lpszMenuName = nullptr;
  classInfo.lpszClassName = LPCWSTR(kAppName);
  classInfo.hIconSm = nullptr;
  return RegisterClassEx(&classInfo);
}

void MSWindowsScreen::destroyClass(ATOM windowClass) const
{
  if (windowClass != 0) {
    UnregisterClass(MAKEINTATOM(windowClass), s_windowInstance);
  }
}

HWND MSWindowsScreen::createWindow(ATOM windowClass, const wchar_t *name) const
{
  HWND window = CreateWindowEx(
      WS_EX_TOPMOST | WS_EX_TRANSPARENT | WS_EX_TOOLWINDOW, MAKEINTATOM(windowClass), name, WS_POPUP, 0, 0, 1, 1,
      nullptr, nullptr, s_windowInstance, nullptr
  );
  if (window == nullptr) {
    LOG_ERR("failed to create window: %d", GetLastError());
    throw ScreenOpenFailureException();
  }
  return window;
}

HWND MSWindowsScreen::createDropWindow(ATOM windowClass, const wchar_t *name) const
{
  // Ported from input-leap MSWindowsScreen::createDropWindow. WS_EX_ACCEPTFILES
  // + registration as an OLE drop target lets this small transparent, top-most
  // window capture the CF_HDROP of a drag forced onto it (see
  // extractDraggingFilename). It is normally hidden and only shown briefly.
  HWND window = CreateWindowEx(
      WS_EX_TOPMOST | WS_EX_TRANSPARENT | WS_EX_ACCEPTFILES, MAKEINTATOM(windowClass), name, WS_POPUP, 0, 0,
      m_dropWindowSize, m_dropWindowSize, nullptr, nullptr, s_windowInstance, nullptr
  );
  if (window == nullptr) {
    LOG_ERR("failed to create drop window: %d", GetLastError());
    throw ScreenOpenFailureException();
  }
  return window;
}

void MSWindowsScreen::destroyWindow(HWND hwnd) const
{
  if (hwnd != nullptr) {
    DestroyWindow(hwnd);
  }
}

void MSWindowsScreen::sendEvent(EventTypes type, void *data)
{
  m_events->addEvent(Event(type, getEventTarget(), data));
}

void MSWindowsScreen::sendClipboardEvent(EventTypes type, ClipboardID id)
{
  ClipboardInfo *info = (ClipboardInfo *)malloc(sizeof(ClipboardInfo));
  if (info == nullptr) {
    LOG_ERR("malloc failed on %s:%s", __FILE__, __LINE__);
    return;
  }
  info->m_id = id;
  info->m_sequenceNumber = m_sequenceNumber;
  sendEvent(type, info);
}

void MSWindowsScreen::handleSystemEvent(const Event &event)
{
  MSG *msg = static_cast<MSG *>(event.getData());
  assert(msg != nullptr);

  if (onPreDispatch(msg->hwnd, msg->message, msg->wParam, msg->lParam)) {
    return;
  }
  TranslateMessage(msg);
  DispatchMessage(msg);
}

void MSWindowsScreen::updateButtons()
{
  int numButtons = GetSystemMetrics(SM_CMOUSEBUTTONS);
  m_buttons[kButtonNone] = false;
  m_buttons[kButtonLeft] = (GetKeyState(VK_LBUTTON) < 0);
  m_buttons[kButtonRight] = (GetKeyState(VK_RBUTTON) < 0);
  m_buttons[kButtonMiddle] = (GetKeyState(VK_MBUTTON) < 0);
  m_buttons[kButtonExtra0 + 0] = (numButtons >= 4) && (GetKeyState(VK_XBUTTON1) < 0);
  m_buttons[kButtonExtra0 + 1] = (numButtons >= 5) && (GetKeyState(VK_XBUTTON2) < 0);
}

IKeyState *MSWindowsScreen::getKeyState() const
{
  return m_keyState;
}

bool MSWindowsScreen::onPreDispatch(HWND hwnd, UINT message, WPARAM wParam, LPARAM lParam)
{
  // handle event
  switch (message) {
  case DESKFLOW_MSG_SCREEN_SAVER:
    return onScreensaver(wParam != 0);

  case DESKFLOW_MSG_DEBUG:
    LOG_VERBOSE("hook: 0x%08x 0x%08x", wParam, lParam);
    return true;
  }

  if (m_isPrimary) {
    return onPreDispatchPrimary(hwnd, message, wParam, lParam);
  }

  return false;
}

bool MSWindowsScreen::onPreDispatchPrimary(HWND, UINT message, WPARAM wParam, LPARAM lParam)
{
  LOG_VERBOSE("handling pre-dispatch primary");

  // handle event
  switch (message) {
  case DESKFLOW_MSG_MARK:
    return onMark(static_cast<uint32_t>(wParam));

  case DESKFLOW_MSG_KEY:
    return onKey(wParam, lParam);

  case DESKFLOW_MSG_MOUSE_BUTTON:
    return onMouseButton(wParam, lParam);

  case DESKFLOW_MSG_MOUSE_MOVE:
    return onMouseMove(static_cast<int32_t>(wParam), static_cast<int32_t>(lParam));

  case DESKFLOW_MSG_MOUSE_WHEEL:
    return onMouseWheel(static_cast<int32_t>(lParam), static_cast<int32_t>(wParam));

  case DESKFLOW_MSG_PRE_WARP: {
    // save position to compute delta of next motion
    saveMousePosition(static_cast<int32_t>(wParam), static_cast<int32_t>(lParam));

    // we warped the mouse.  discard events until we find the
    // matching post warp event.  see warpCursorNoFlush() for
    // where the events are sent.  we discard the matching
    // post warp event and can be sure we've skipped the warp
    // event.
    MSG msg;
    do {
      GetMessage(&msg, nullptr, DESKFLOW_MSG_MOUSE_MOVE, DESKFLOW_MSG_POST_WARP);
    } while (msg.message != DESKFLOW_MSG_POST_WARP);
  }
    return true;

  case DESKFLOW_MSG_POST_WARP:
    LOG_WARN("unmatched post warp");
    return true;

  case WM_HOTKEY:
    // we discard these messages.  we'll catch the hot key in the
    // regular key event handling, where we can detect both key
    // press and release.  we only register the hot key so no other
    // app will act on the key combination.
    break;
  }

  return false;
}

bool MSWindowsScreen::onEvent(HWND, UINT msg, WPARAM wParam, LPARAM lParam, LRESULT *result)
{
  switch (msg) {

  case WM_CLIPBOARDUPDATE: {
    DWORD clipboardSequenceNumber = GetClipboardSequenceNumber();
    LOG_DEBUG("clipboard update: sequence number %d, current %d", clipboardSequenceNumber, m_clipboardSequenceNumber);

    if (clipboardSequenceNumber && (clipboardSequenceNumber != m_clipboardSequenceNumber)) {
      m_clipboardSequenceNumber = clipboardSequenceNumber;
      onClipboardChange();
    }
    return 0; // message processed
  }

  case WM_DISPLAYCHANGE:
    return onDisplayChange();

  /* On windows 10 we don't receive WM_POWERBROADCAST after sleep.
   We receive only WM_TIMECHANGE hence this message is used to resume.*/
  case WM_TIMECHANGE:
    m_events->addEvent( //
        Event(EventTypes::ScreenResume, getEventTarget(), nullptr, Event::EventFlags::DeliverImmediately)
    );
    break;

  case WM_POWERBROADCAST:
    switch (wParam) {
    case PBT_APMRESUMEAUTOMATIC:
    case PBT_APMRESUMECRITICAL:
    case PBT_APMRESUMESUSPEND:
      m_events->addEvent(
          Event(EventTypes::ScreenResume, getEventTarget(), nullptr, Event::EventFlags::DeliverImmediately)
      );
      break;

    case PBT_APMSUSPEND:
      m_events->addEvent(
          Event(EventTypes::ScreenSuspend, getEventTarget(), nullptr, Event::EventFlags::DeliverImmediately)
      );
      break;
    }
    *result = TRUE;
    return true;

  case WM_DEVICECHANGE: {
    // re-run mouse keys setup in case a mouse was plugged in or unplugged; i.e. if a mouse was
    // unplugged from the client, make sure the mouse cursor is still visible.
    // the device change event happens for every discreet hardware change, so if you're using a
    // usb switcher, this generates many device change events. it would be nice to log here but
    // the log would be too noisy.
    setupMouseKeys();
  } break;

  case WM_SETTINGCHANGE:
    // sometimes fired when the mouse keys setting is changed, but doesn't seem very reliable.
    // these events may arrive at any time (e.g. when the program is shutting down) if the message
    // loop stops being processed for any reason. this may be a bug or something out of our control.
    // forcing mouse keys on may help in scenarios where mouse keys are being turned off by another app.
    if (wParam == SPI_SETMOUSEKEYS) {
      LOG_DEBUG("mouse keys setting was changed");
      setupMouseKeys();
    }
    break;
  }

  return false;
}

bool MSWindowsScreen::onMark(uint32_t mark)
{
  m_markReceived = mark;
  return true;
}

bool MSWindowsScreen::onKey(WPARAM wParam, LPARAM lParam)
{
  static const KeyModifierMask s_ctrlAlt = KeyModifierControl | KeyModifierAlt;

  LOG_VERBOSE(
      "event: Key char=%d, vk=0x%02x, nagr=%d, lParam=0x%08x", (wParam & 0xffffu), (wParam >> 16) & 0xffu,
      (wParam & 0x1000000u) ? 1 : 0, lParam
  );

  // get event info
  KeyButton button = (KeyButton)((lParam & 0x01ff0000) >> 16);
  bool down = ((lParam & 0x80000000u) == 0x00000000u);
  bool wasDown = isKeyDown(button);
  KeyModifierMask oldState = pollActiveModifiers();

  // check for autorepeat
  if (m_keyState->testAutoRepeat(down, (lParam & 0x40000000u), button)) {
    lParam |= 0x40000000u;
  }

  // if the button is zero then guess what the button should be.
  // these are badly synthesized key events and logitech software
  // that maps mouse buttons to keys is known to do this.
  // alternatively, we could just throw these events out.
  if (button == 0) {
    button = m_keyState->virtualKeyToButton((wParam >> 16) & 0xffu);
    if (button == 0) {
      return true;
    }
    wasDown = isKeyDown(button);
  }

  // record keyboard state
  m_keyState->onKey(button, down, oldState);

  if (!down && m_isPrimary && !m_isOnScreen) {
    PrimaryKeyDownList::iterator find = std::find(m_primaryKeyDownList.begin(), m_primaryKeyDownList.end(), button);
    if (find != m_primaryKeyDownList.end()) {
      LOG_VERBOSE("release key button %d on primary", *find);
      m_hook.setMode(kHOOK_WATCH_JUMP_ZONE);
      fakeLocalKey(*find, false);
      m_primaryKeyDownList.erase(find);
      m_hook.setMode(kHOOK_RELAY_EVENTS);
      return true;
    }
  }

  // windows doesn't tell us the modifier key state on mouse or key
  // events so we have to figure it out.  most apps would use
  // GetKeyState() or even GetAsyncKeyState() for that but we can't
  // because our hook doesn't pass on key events for several modifiers.
  // it can't otherwise the system would interpret them normally on
  // the primary screen even when on a secondary screen.  so tapping
  // alt would activate menus and tapping the windows key would open
  // the start menu.  if you don't pass those events on in the hook
  // then GetKeyState() understandably doesn't reflect the effect of
  // the event.  curiously, neither does GetAsyncKeyState(), which is
  // surprising.
  //
  // so anyway, we have to track the modifier state ourselves for
  // at least those modifiers we don't pass on.  pollActiveModifiers()
  // does that but we have to update the keyboard state before calling
  // pollActiveModifiers() to get the right answer.  but the only way
  // to set the modifier state or to set the up/down state of a key
  // is via onKey().  so we have to call onKey() twice.
  KeyModifierMask state = pollActiveModifiers();
  m_keyState->onKey(button, down, state);

  // check for hot keys
  if (oldState != state) {
    // modifier key was pressed/released
    if (onHotKey(0, lParam)) {
      return true;
    }
  } else {
    // non-modifier was pressed/released
    if (onHotKey(wParam, lParam)) {
      return true;
    }
  }

  // stop sending modifier keys over and over again
  // but allow up to be sent if the previous state was down
  // to prevent stuck modifier keys on the client
  if (down && wasDown && isModifierRepeat(oldState, state, wParam)) {
    return true;
  }

  // ignore message if posted prior to last mark change
  if (!ignore()) {
    // check for ctrl+alt+del.  we do not want to pass that to the
    // client.  the user can use ctrl+alt+pause to emulate it.
    UINT virtKey = ((wParam >> 16) & 0xffu);
    if (virtKey == VK_DELETE && (state & s_ctrlAlt) == s_ctrlAlt) {
      LOG_DEBUG("discard ctrl+alt+del");
      return true;
    }

    // check for ctrl+alt+del emulation
    if ((virtKey == VK_PAUSE || virtKey == VK_CANCEL) && (state & s_ctrlAlt) == s_ctrlAlt) {
      LOG_DEBUG("emulate ctrl+alt+del");
      // switch wParam and lParam to be as if VK_DELETE was
      // pressed or released.  when mapping the key we require that
      // we not use AltGr (the 0x10000 flag in wParam) and we not
      // use the keypad delete key (the 0x01000000 flag in lParam).
      wParam = (VK_DELETE << 16) | 0x01000000u;
      lParam &= 0xfe000000;
      lParam |= m_keyState->virtualKeyToButton(VK_DELETE) << 16;
      lParam |= 0x01000001;
    }

    // process key
    KeyModifierMask mask;
    KeyID key = m_keyState->mapKeyFromEvent(wParam, lParam, &mask);
    const KeyButton mappedButton = static_cast<KeyButton>((lParam & 0x01ff0000u) >> 16);
    if (mappedButton != 0) {
      // Ctrl+Alt+Del emulation deliberately substitutes its button in lParam.
      // Otherwise retain the virtual-key fallback above for malformed input
      // that has no scan code, so its key-up matches the key-down on clients.
      button = mappedButton;
    }
    if (key != kKeyNone) {
      // do it
      m_keyState->sendKeyEvent(
          getEventTarget(), ((lParam & 0x80000000u) == 0), ((lParam & 0x40000000u) != 0), key, mask,
          (int32_t)(lParam & 0xffff), button
      );
    } else {
      LOG_VERBOSE("cannot map key");
    }
  }

  return true;
}

bool MSWindowsScreen::onHotKey(WPARAM wParam, LPARAM lParam)
{
  // get the key info
  KeyModifierMask state = getActiveModifiers();
  UINT virtKey = ((wParam >> 16) & 0xffu);
  UINT modifiers = 0;
  if ((state & KeyModifierShift) != 0) {
    modifiers |= MOD_SHIFT;
  }
  if ((state & KeyModifierControl) != 0) {
    modifiers |= MOD_CONTROL;
  }
  if ((state & KeyModifierAlt) != 0) {
    modifiers |= MOD_ALT;
  }
  if ((state & KeyModifierSuper) != 0) {
    modifiers |= MOD_WIN;
  }

  // find the hot key id
  HotKeyToIDMap::const_iterator i = m_hotKeyToIDMap.find(HotKeyItem(virtKey, modifiers));
  if (i == m_hotKeyToIDMap.end()) {
    return false;
  }

  // find what kind of event
  EventTypes type;
  if ((lParam & 0x80000000u) == 0u) {
    if ((lParam & 0x40000000u) != 0u) {
      // ignore key repeats but it counts as a hot key
      return true;
    }
    type = EventTypes::PrimaryScreenHotkeyDown;
  } else {
    type = EventTypes::PrimaryScreenHotkeyUp;
  }

  // generate event
  m_events->addEvent(Event(type, getEventTarget(), HotKeyInfo::alloc(i->second)));

  return true;
}

bool MSWindowsScreen::onMouseButton(WPARAM wParam, LPARAM lParam)
{
  // get which button
  bool pressed = mapPressFromEvent(wParam, lParam);
  ButtonID button = mapButtonFromEvent(wParam, lParam);

  // keep our shadow key state up to date
  if (button >= kButtonLeft && button <= kButtonExtra0 + 1) {
    m_buttons[button] = pressed;
  }

  // Cross-screen file drag (source side): a fresh left-button press starts a new
  // potential drag, so clear any stale captured drag; a release ends dragging.
  if (button == kButtonLeft) {
    m_draggingStarted = false;
    if (pressed) {
      m_draggingFilename.clear();
      m_draggingFileList.clear();
    }
  }

  // ignore message if posted prior to last mark change
  if (!ignore()) {
    KeyModifierMask mask = m_keyState->getActiveModifiers();
    if (pressed) {
      LOG_VERBOSE("event: button press button=%d", button);
      if (button != kButtonNone) {
        sendEvent(EventTypes::PrimaryScreenButtonDown, ButtonInfo::alloc(button, mask));
      }
    } else {
      LOG_VERBOSE("event: button release button=%d", button);
      if (button != kButtonNone) {
        sendEvent(EventTypes::PrimaryScreenButtonUp, ButtonInfo::alloc(button, mask));
      }
    }
  }

  return true;
}

// here's how mouse movements are sent across the network to a client:
//   1. deskflow checks the mouse position on server screen
//   2. records the delta (current x,y minus last x,y)
//   3. records the current x,y as "last" (so we can calc delta next time)
//   4. on the server, puts the cursor back to the center of the screen
//      - remember the cursor is hidden on the server at this point
//      - this actually records the current x,y as "last" a second time (it
//      seems)
//   5. sends the delta movement to the client (could be +1,+1 or -1,+4 for
//   example)
bool MSWindowsScreen::onMouseMove(int32_t mx, int32_t my)
{
  // compute motion delta (relative to the last known
  // mouse position)
  int32_t x = mx - m_xCursor;
  int32_t y = my - m_yCursor;

  LOG_VERBOSE("mouse move - motion delta: %+d=(%+d - %+d),%+d=(%+d - %+d)", x, mx, m_xCursor, y, my, m_yCursor);

  // ignore if the mouse didn't move or if message posted prior
  // to last mark change.
  if (ignore() || (x == 0 && y == 0)) {
    return true;
  }

  // save position to compute delta of next motion
  saveMousePosition(mx, my);

  if (m_isOnScreen) {
    // Cross-screen file drag (source side): a left-button-held motion while the
    // cursor is on this screen means the user is dragging something. Mark it so
    // the server/client layer knows to hand the drag to the peer at the edge.
    // Proven Synergy/Input Leap detection (see upstream onMouseMove).
    if (m_buttons[kButtonLeft]) {
      m_draggingStarted = true;
    }

    // motion on primary screen
    sendEvent(EventTypes::PrimaryScreenMotionOnPrimary, MotionInfo::alloc(m_xCursor, m_yCursor));
  } else {
    // the motion is on the secondary screen, so we warp mouse back to
    // center on the server screen. if we don't do this, then the mouse
    // will always try to return to the original entry point on the
    // secondary screen.
    LOG_VERBOSE("centering cursor on motion: %+d,%+d", m_xCenter, m_yCenter);
    warpCursorNoFlush(m_xCenter, m_yCenter);

    // examine the motion.  if it's about the distance
    // from the center of the screen to an edge then
    // it's probably a bogus motion that we want to
    // ignore (see warpCursorNoFlush() for a further
    // description).
    static int32_t bogusZoneSize = 10;
    if (-x + bogusZoneSize > m_xCenter - m_x || x + bogusZoneSize > m_x + m_w - m_xCenter ||
        -y + bogusZoneSize > m_yCenter - m_y || y + bogusZoneSize > m_y + m_h - m_yCenter) {

      LOG_DEBUG("dropped bogus delta motion: %+d,%+d", x, y);
    } else {
      // send motion
      sendEvent(EventTypes::PrimaryScreenMotionOnSecondary, MotionInfo::alloc(x, y));
    }
  }

  return true;
}

bool MSWindowsScreen::onMouseWheel(int32_t xDelta, int32_t yDelta)
{
  // ignore message if posted prior to last mark change
  if (!ignore()) {
    LOG_VERBOSE("event: button wheel delta=%+d,%+d", xDelta, yDelta);
    sendEvent(EventTypes::PrimaryScreenWheel, WheelInfo::alloc(xDelta, yDelta));
  }
  return true;
}

bool MSWindowsScreen::onScreensaver(bool activated)
{
  // ignore this message if there are any other screen saver
  // messages already in the queue.  this is important because
  // our checkStarted() function has a deliberate delay, so it
  // can't respond to events at full CPU speed and will fall
  // behind if a lot of screen saver events are generated.
  // that can easily happen because windows will continually
  // send SC_SCREENSAVE until the screen saver starts, even if
  // the screen saver is disabled!
  MSG msg;
  if (PeekMessage(&msg, nullptr, DESKFLOW_MSG_SCREEN_SAVER, DESKFLOW_MSG_SCREEN_SAVER, PM_NOREMOVE)) {
    return true;
  }

  if (activated) {
    if (!m_screensaverActive && m_screensaver->checkStarted(DESKFLOW_MSG_SCREEN_SAVER, FALSE, 0)) {
      m_screensaverActive = true;
      sendEvent(EventTypes::PrimaryScreenSaverActivated);
    }
  } else {
    if (m_screensaverActive) {
      m_screensaverActive = false;
      sendEvent(EventTypes::PrimaryScreenSaverDeactivated);
    }
  }

  return true;
}

bool MSWindowsScreen::onDisplayChange()
{
  // screen resolution may have changed.  save old shape.
  int32_t xOld = m_x, yOld = m_y, wOld = m_w, hOld = m_h;

  // update shape
  updateScreenShape();

  // do nothing if resolution hasn't changed
  if (xOld != m_x || yOld != m_y || wOld != m_w || hOld != m_h) {
    if (m_isPrimary) {
      if (!m_isOnScreen) {
        LOG_VERBOSE("centering cursor on display change: %+d, %+d", m_xCenter, m_yCenter);
        warpCursor(m_xCenter, m_yCenter);
      }

      // tell hook about resize if on screen
      else {
        m_hook.setZone(m_x, m_y, m_w, m_h, getJumpZoneSize());
      }
    }

    // send new screen info
    sendEvent(EventTypes::ScreenShapeChanged);

    LOG_DEBUG("screen shape: %d,%d %dx%d %s", m_x, m_y, m_w, m_h, m_multimon ? "(multi-monitor)" : "");
  }

  return true;
}

void MSWindowsScreen::onClipboardChange()
{
  // now notify client that somebody changed the clipboard (unless
  // we're the owner).
  if (!MSWindowsClipboard::isOwnedByDeskflow()) {
    if (m_ownClipboard) {
      LOG_DEBUG("clipboard changed: lost ownership");
      m_ownClipboard = false;
      sendClipboardEvent(EventTypes::ClipboardGrabbed, kClipboardClipboard);
      sendClipboardEvent(EventTypes::ClipboardGrabbed, kClipboardSelection);
    }
  } else if (!m_ownClipboard) {
    LOG_DEBUG("clipboard changed: %s owned", kAppId);
    m_ownClipboard = true;
  }
}

void MSWindowsScreen::warpCursorNoFlush(int32_t x, int32_t y)
{
  // send an event that we can recognize before the mouse warp
  PostThreadMessage(GetCurrentThreadId(), DESKFLOW_MSG_PRE_WARP, x, y);

  // warp mouse.  hopefully this inserts a mouse motion event
  // between the previous message and the following message.
  setThisCursorPos(x, y);

  // check to see if the mouse pos was set correctly
  POINT cursorPos;
  getThisCursorPos(&cursorPos);

  // there is a bug or round error in SetCursorPos and GetCursorPos on
  // a high DPI setting. The check here is for Vista/7 login screen.
  // since this feature is mainly for client, so only check on client.
  if (!isPrimary()) {
    if ((cursorPos.x != x) && (cursorPos.y != y)) {
      LOG_DEBUG("function 'SetCursorPos' failed; trying 'fakeMouseMove'");
      LOG_DEBUG("cursor pos %d, %d expected pos %d, %d", cursorPos.x, cursorPos.y, x, y);
      // when at Vista/7 login screen, SetCursorPos does not work (which could
      // be an MS security feature). instead we can use fakeMouseMove, which
      // calls mouse_event. IMPORTANT: as of implementing this function, it has
      // an annoying side effect; instead of the mouse returning to the correct
      // exit point, it returns to the center of the screen. this could have
      // something to do with the center screen warping technique used (see
      // comments for onMouseMove definition).
      fakeMouseMove(x, y);
    }
  }

  // yield the CPU.  there's a race condition when warping:
  //   a hardware mouse event occurs
  //   the mouse hook is not called because that process doesn't have the CPU
  //   we send PRE_WARP, SetCursorPos(), send POST_WARP
  //   we process all of those events and update m_x, m_y
  //   we finish our time slice
  //   the hook is called
  //   the hook sends us a mouse event from the pre-warp position
  //   we get the CPU
  //   we compute a bogus warp
  // we need the hook to process all mouse events that occur
  // before we warp before we do the warp but i'm not sure how
  // to guarantee that.  yielding the CPU here may reduce the
  // chance of undesired behavior.  we'll also check for very
  // large motions that look suspiciously like about half width
  // or height of the screen.
  Arch::sleep(0.0);

  // send an event that we can recognize after the mouse warp
  PostThreadMessage(GetCurrentThreadId(), DESKFLOW_MSG_POST_WARP, 0, 0);
}

void MSWindowsScreen::nextMark()
{
  // next mark
  ++m_mark;

  // mark point in message queue where the mark was changed
  PostThreadMessage(GetCurrentThreadId(), DESKFLOW_MSG_MARK, m_mark, 0);
}

bool MSWindowsScreen::ignore() const
{
  return (m_mark != m_markReceived);
}

void MSWindowsScreen::updateScreenShape()
{
  // get shape and center
  m_w = GetSystemMetrics(SM_CXVIRTUALSCREEN);
  m_h = GetSystemMetrics(SM_CYVIRTUALSCREEN);
  m_x = GetSystemMetrics(SM_XVIRTUALSCREEN);
  m_y = GetSystemMetrics(SM_YVIRTUALSCREEN);
  m_xCenter = GetSystemMetrics(SM_CXSCREEN) >> 1;
  m_yCenter = GetSystemMetrics(SM_CYSCREEN) >> 1;

  // check for multiple monitors
  m_multimon = (m_w != GetSystemMetrics(SM_CXSCREEN) || m_h != GetSystemMetrics(SM_CYSCREEN));

  // tell the desks
  m_desks->setShape(m_x, m_y, m_w, m_h, m_xCenter, m_yCenter, m_multimon);
}

void MSWindowsScreen::handleFixes()
{
  // fix clipboard chain
  fixClipboardViewer();

  // update keys if keyboard layouts have changed
  if (m_keyState->didGroupsChange()) {
    updateKeys();
  }
}

void MSWindowsScreen::fixClipboardViewer()
{
  // XXX -- disable this code for now.  somehow it can cause an infinite
  // recursion in the WM_DRAWCLIPBOARD handler.  either we're sending
  // the message to our own window or some window farther down the chain
  // forwards the message to our window or a window farther up the chain.
  // i'm not sure how that could happen.  the m_nextClipboardWindow = nullptr
  // was not in the code that infinite loops and may fix the bug but i
  // doubt it.
  /*
      ChangeClipboardChain(m_window, m_nextClipboardWindow);
      m_nextClipboardWindow = nullptr;
      m_nextClipboardWindow = SetClipboardViewer(m_window);
  */
}

void MSWindowsScreen::enableSpecialKeys(bool enable) const
{
}

ButtonID MSWindowsScreen::mapButtonFromEvent(WPARAM msg, LPARAM button) const
{
  switch (msg) {
  case WM_LBUTTONDOWN:
  case WM_LBUTTONDBLCLK:
  case WM_LBUTTONUP:
  case WM_NCLBUTTONDOWN:
  case WM_NCLBUTTONDBLCLK:
  case WM_NCLBUTTONUP:
    return kButtonLeft;

  case WM_MBUTTONDOWN:
  case WM_MBUTTONDBLCLK:
  case WM_MBUTTONUP:
  case WM_NCMBUTTONDOWN:
  case WM_NCMBUTTONDBLCLK:
  case WM_NCMBUTTONUP:
    return kButtonMiddle;

  case WM_RBUTTONDOWN:
  case WM_RBUTTONDBLCLK:
  case WM_RBUTTONUP:
  case WM_NCRBUTTONDOWN:
  case WM_NCRBUTTONDBLCLK:
  case WM_NCRBUTTONUP:
    return kButtonRight;

  case WM_XBUTTONDOWN:
  case WM_XBUTTONDBLCLK:
  case WM_XBUTTONUP:
  case WM_NCXBUTTONDOWN:
  case WM_NCXBUTTONDBLCLK:
  case WM_NCXBUTTONUP:
    switch (button) {
    case XBUTTON1:
      if (GetSystemMetrics(SM_CMOUSEBUTTONS) >= 4) {
        return kButtonExtra0;
      }
      break;

    case XBUTTON2:
      if (GetSystemMetrics(SM_CMOUSEBUTTONS) >= 5) {
        return kButtonExtra1;
      }
      break;
    }
    return kButtonNone;

  default:
    return kButtonNone;
  }
}

bool MSWindowsScreen::mapPressFromEvent(WPARAM msg, LPARAM) const
{
  switch (msg) {
  case WM_LBUTTONDOWN:
  case WM_MBUTTONDOWN:
  case WM_RBUTTONDOWN:
  case WM_XBUTTONDOWN:
  case WM_LBUTTONDBLCLK:
  case WM_MBUTTONDBLCLK:
  case WM_RBUTTONDBLCLK:
  case WM_XBUTTONDBLCLK:
  case WM_NCLBUTTONDOWN:
  case WM_NCMBUTTONDOWN:
  case WM_NCRBUTTONDOWN:
  case WM_NCXBUTTONDOWN:
  case WM_NCLBUTTONDBLCLK:
  case WM_NCMBUTTONDBLCLK:
  case WM_NCRBUTTONDBLCLK:
  case WM_NCXBUTTONDBLCLK:
    return true;

  case WM_LBUTTONUP:
  case WM_MBUTTONUP:
  case WM_RBUTTONUP:
  case WM_XBUTTONUP:
  case WM_NCLBUTTONUP:
  case WM_NCMBUTTONUP:
  case WM_NCRBUTTONUP:
  case WM_NCXBUTTONUP:
    return false;

  default:
    return false;
  }
}

void MSWindowsScreen::updateKeysCB(const void *)
{
  // record which keys we think are down
  bool down[IKeyState::s_numButtons];
  bool sendFixes = (isPrimary() && !m_isOnScreen);
  if (sendFixes) {
    for (KeyButton i = 0; i < IKeyState::s_numButtons; ++i) {
      down[i] = m_keyState->isKeyDown(i);
    }
  }

  // update layouts if necessary
  if (m_keyState->didGroupsChange()) {
    PlatformScreen::updateKeyMap();
  }

  // now update the keyboard state
  PlatformScreen::updateKeyState();

  // now see which keys we thought were down but now think are up.
  // send key releases for these keys to the active client.
  if (sendFixes) {
    KeyModifierMask mask = pollActiveModifiers();
    for (KeyButton i = 0; i < IKeyState::s_numButtons; ++i) {
      if (down[i] && !m_keyState->isKeyDown(i)) {
        m_keyState->sendKeyEvent(getEventTarget(), false, false, kKeyNone, mask, 1, i);
      }
    }
  }
}

void MSWindowsScreen::setupMouseKeys()
{
  // we only need to enable the mouse keys feature when on a secondary screen.
  // this tricks windows into showing the mouse cursor when there is no real mouse.
  if (m_isPrimary) {
    // silent return to avoid noise.
    return;
  }

  // this is the case when there is some kind of a mouse (real or simulated by mouse keys).
  m_hasMouse = (GetSystemMetrics(SM_MOUSEPRESENT) != 0);
  if (m_hasMouse) {
    // silent return to avoid noise.
    return;
  }

  // prevents mouse keys being configured again when the program is shutting down since this function
  // is called based on system events such as system setting changes or hardware changes which
  // can occur at any time.
  if (!m_isEnabled) {
    LOG_DEBUG("mouse keys setup skipped, screen is not enabled");
    return;
  }

  m_mouseKeys.cbSize = sizeof(m_mouseKeys);
  m_gotMouseKeys = (SystemParametersInfo(SPI_GETMOUSEKEYS, m_mouseKeys.cbSize, &m_mouseKeys, 0) != 0);
  if (!m_gotMouseKeys) {
    LOG_ERR("unable to get old mouse keys settings, error: %d", GetLastError());
    return;
  }

  updateMouseKeys();
}

void MSWindowsScreen::updateMouseKeys()
{
  if (m_hasMouse || !m_gotMouseKeys || m_isPrimary) {
    // silent return to avoid noise.
    return;
  }

  DWORD oldFlags = m_mouseKeys.dwFlags;

  // turn on the windows mouse keys accessibility feature.
  // this is referred to as 'MouseKeys' in the docs.
  // makes the mouse cursor visible if there is no real mouse.
  //
  // historically, we would only set the `MKF_REPLACENUMBERS` flag when num lock is on.
  // however, this was a strange hidden feature that the user will most likely not expect;
  // it's probably more sensible to always set the `MKF_REPLACENUMBERS` flag, so that when the
  // mouse keys feature is left on after the program exits, the num pad on a local keyboard still
  // types numbers instead of moving the mouse cursor around (which would surprise most users).
  //
  // by default, windows 11 shows the mouse keys status in the system tray, but turning this on
  // might actually cause confusion for users who are not familiar with the mouse keys feature.
  m_mouseKeys.dwFlags = MKF_AVAILABLE | MKF_MOUSEKEYSON | MKF_REPLACENUMBERS;

  // only update the mouse keys settings if different to avoid noise.
  if (oldFlags == m_mouseKeys.dwFlags) {
    // silent return to avoid noise.
    return;
  }

  // we used to restore the old mouse keys settings but toggling the mouse keys feature on and off
  // causes the mouse cursor to be come stuck in an invisible state even when there is a real mouse.
  // we may want to reintroduce it (restore old mouse keys flags) as a user option in the future,
  // e.g. for users who use periodically use their windows client directly and use the numpad for cursor keys.
  LOG_INFO("enabling mouse keys for cursor visibility");
  LOG_DEBUG("setting mouse keys flags: 0x%08x", m_mouseKeys.dwFlags);
  const auto ok = SystemParametersInfo(SPI_SETMOUSEKEYS, m_mouseKeys.cbSize, &m_mouseKeys, SPIF_SENDCHANGE);
  if (!ok) {
    LOG_ERR("failed to set mouse keys, error: %d", GetLastError());
  } else {
    LOG_VERBOSE("mouse keys enabled successfully");
  }
}

LRESULT CALLBACK MSWindowsScreen::wndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam)
{
  assert(s_screen != nullptr);

  LRESULT result = 0;
  if (!s_screen->onEvent(hwnd, msg, wParam, lParam, &result)) {
    result = DefWindowProc(hwnd, msg, wParam, lParam);
  }

  return result;
}

void MSWindowsScreen::fakeLocalKey(KeyButton button, bool press) const
{
  INPUT input;
  input.type = INPUT_KEYBOARD;
  input.ki.wVk = m_keyState->mapButtonToVirtualKey(button);
  DWORD pressFlag = press ? KEYEVENTF_EXTENDEDKEY : KEYEVENTF_KEYUP;
  input.ki.dwFlags = pressFlag;
  input.ki.time = 0;
  input.ki.dwExtraInfo = 0;
  SendInput(1, &input, sizeof(input));
}

//
// MSWindowsScreen -- DeskBridge cross-screen drag-and-drop (OLE)
//
// Source side (isDraggingStarted / getDraggingFilename / cancelLocalDrag) uses
// the proven Synergy/Input Leap OLE capture technique (extractDraggingFilename +
// MSWindowsDropTarget). The target-side synthetic drag (fakeDraggingFiles) is
// DeskBridge-original and still needs validation on real Windows hardware; see
// the note in the anonymous namespace at the top of this file.
//

bool MSWindowsScreen::isDraggingStarted()
{
  return m_draggingStarted;
}

void MSWindowsScreen::extractDraggingFilename()
{
  // Proven Synergy/Input Leap technique (ported from input-leap
  // src/lib/platform/MSWindowsScreen.cpp getDraggingFilename): teleport the tiny
  // transparent accept-files drop window under the cursor, then force the
  // in-progress OLE drag to drop onto it (inject Escape + release left button).
  // MSWindowsDropTarget::DragEnter records the CF_HDROP path, which we poll for.
  //
  // Injecting Escape + button-up ALSO ends the local OS drag, so this doubles as
  // the "cancel the local drag" step of the handoff. Runs on the event thread
  // and blocks up to ~0.5s while polling; this mirrors upstream behaviour.
  //
  // Idempotent: the server/client layer samples the drag more than once around a
  // handoff (cancelLocalDrag + getDraggingFileList / getDraggingFilename). Once a
  // name has been captured, don't re-inject and re-poll (the drag is already
  // gone, so a second attempt would only clear a good capture).
  if (!m_draggingStarted || !m_draggingFilename.empty()) {
    return;
  }

  m_dropTarget->clearDraggingFilename();
  m_draggingFilename.clear();

  const int halfSize = m_dropWindowSize / 2;
  int32_t xPos = m_isPrimary ? m_xCursor : m_xCenter;
  int32_t yPos = m_isPrimary ? m_yCursor : m_yCenter;
  xPos = (xPos - halfSize) < 0 ? 0 : xPos - halfSize;
  yPos = (yPos - halfSize) < 0 ? 0 : yPos - halfSize;
  SetWindowPos(m_dropWindow, HWND_TOPMOST, xPos, yPos, m_dropWindowSize, m_dropWindowSize, SWP_SHOWWINDOW);

  // A tiny sleep here makes the DragEnter event on m_dropWindow trigger much
  // more consistently (per upstream comment).
  ARCH->sleep(0.05);
  fakeKeyDown(kKeyEscape, 8192, 1, "");
  fakeKeyUp(1);
  fakeMouseButton(kButtonLeft, false);

  std::string filename;
  const double timeout = ARCH->time() + 0.5;
  while (ARCH->time() < timeout) {
    ARCH->sleep(0.05);
    filename = m_dropTarget->getDraggingFilename();
    if (!filename.empty()) {
      break;
    }
  }

  ShowWindow(m_dropWindow, SW_HIDE);

  if (!filename.empty()) {
    m_draggingFilename = filename;
    m_draggingFileList.clear();
    m_draggingFileList.emplace_back(filename, 0);
    LOG_INFO("drag: captured dragged file: %s", filename.c_str());
  } else {
    LOG_ERR("drag: failed to get drag file name from OLE");
  }
}

std::string MSWindowsScreen::getDraggingFilename()
{
  // If the handoff already captured the name (via cancelLocalDrag ->
  // extractDraggingFilename), return it; otherwise capture it now.
  if (m_draggingFilename.empty()) {
    extractDraggingFilename();
  }
  return m_draggingFilename;
}

DragFileList MSWindowsScreen::getDraggingFileList()
{
  // The proven capture path is single-file; if nothing is cached yet, trigger a
  // capture so callers that only use this accessor still get the file. The
  // server/client layer also falls back to getDraggingFilename() when this is
  // empty (see Server::sendDragInfoToClient / Client::sendDragToServer).
  if (m_draggingFileList.empty() && m_draggingFilename.empty()) {
    extractDraggingFilename();
  }
  return m_draggingFileList;
}

const std::string &MSWindowsScreen::getDropTarget() const
{
  // Ported from input-leap getDropTarget: default to the user's Desktop when no
  // explicit drop directory has been set.
  if (m_dropTargetPath.empty()) {
    wchar_t desktopPath[MAX_PATH];
    if (SUCCEEDED(SHGetFolderPathW(nullptr, CSIDL_DESKTOPDIRECTORY, nullptr, 0, desktopPath))) {
      const int n = WideCharToMultiByte(CP_UTF8, 0, desktopPath, -1, nullptr, 0, nullptr, nullptr);
      if (n > 1) {
        std::string s(static_cast<size_t>(n - 1), '\0');
        WideCharToMultiByte(CP_UTF8, 0, desktopPath, -1, s.data(), n, nullptr, nullptr);
        m_dropTargetPath = std::move(s);
        LOG_INFO("using desktop for drop target: %s", m_dropTargetPath.c_str());
      }
    } else {
      LOG_ERR("failed to get desktop path, no drop target available, error=%d", GetLastError());
    }
  }
  return m_dropTargetPath;
}

void MSWindowsScreen::setDropTarget(const std::string &target)
{
  m_dropTargetPath = target;
}

void MSWindowsScreen::cancelLocalDrag()
{
  // End the local OS drag on this (source) machine after its files have been
  // handed to the peer, so the file is not also dropped/moved here. In our
  // handoff order the server calls this just before sampling getDraggingFilename
  // (see Server::switchScreen), so we capture the dragged name here (which also
  // ends the drag via the injected Escape + button-up) and keep it cached, and
  // deliberately keep m_draggingStarted true so the subsequent isDraggingStarted
  // sample still fires the handoff.
  if (m_draggingStarted) {
    extractDraggingFilename();
  }
}

void MSWindowsScreen::fakeDraggingFiles(const DragFileList &fileList)
{
  // Target side: start a synthetic OLE drag carrying the received file(s) under
  // the (relayed) pointer, so they drop into whatever window the user releases
  // over. Equivalent to XWindowsScreen::fakeDraggingFiles (XDND source) and the
  // macOS drop path.
  if (fileList.empty()) {
    return;
  }
  // never run two synthetic drags at once
  stopDragThread();
  m_dragCancel = false;
  m_dragActive = true;
  m_dragThread = std::thread(&MSWindowsScreen::runDragThread, this, fileList);
  LOG_INFO("drag: started synthetic drag of %zu file(s)", fileList.size());
}

void MSWindowsScreen::runDragThread(DragFileList files)
{
  // DoDragDrop() is a blocking modal loop and needs its own OLE (STA) apartment,
  // so it runs on this dedicated worker thread rather than the input loop.
  const HRESULT init = OleInitialize(nullptr);

  HGLOBAL hdrop = createHDrop(files);
  if (hdrop != nullptr) {
    auto *data = new FileDataObject(hdrop); // owns hdrop
    auto *source = new FileDropSource(&m_dragCancel);
    DWORD effect = 0;
    const HRESULT hr = DoDragDrop(data, source, DROPEFFECT_COPY | DROPEFFECT_MOVE, &effect);
    if (hr == DRAGDROP_S_DROP) {
      LOG_INFO("drag: synthetic drop completed (effect=0x%lx)", effect);
    } else if (hr == DRAGDROP_S_CANCEL) {
      LOG_INFO("drag: synthetic drag cancelled");
    } else {
      LOG_DEBUG("drag: DoDragDrop returned 0x%08lx", hr);
    }
    data->Release();
    source->Release();
  } else {
    LOG_WARN("drag: could not build CF_HDROP payload; synthetic drag aborted");
  }

  if (SUCCEEDED(init)) {
    OleUninitialize();
  }
  m_dragActive = false;
}

void MSWindowsScreen::stopDragThread()
{
  if (m_dragThread.joinable()) {
    m_dragCancel = true; // FileDropSource::QueryContinueDrag will cancel
    m_dragThread.join();
  }
  m_dragActive = false;
  m_dragCancel = false;
}

//
// MSWindowsScreen::HotKeyItem
//

MSWindowsScreen::HotKeyItem::HotKeyItem(UINT keycode, UINT mask) : m_keycode(keycode), m_mask(mask)
{
  // do nothing
}

UINT MSWindowsScreen::HotKeyItem::getVirtualKey() const
{
  return m_keycode;
}

bool MSWindowsScreen::HotKeyItem::operator<(const HotKeyItem &x) const
{
  return (m_keycode < x.m_keycode || (m_keycode == x.m_keycode && m_mask < x.m_mask));
}

std::string MSWindowsScreen::getSecureInputApp() const
{
  // ignore on Windows
  return "";
}

bool MSWindowsScreen::isModifierRepeat(KeyModifierMask oldState, KeyModifierMask state, WPARAM wParam) const
{
  bool result = false;

  if (oldState == state && state != 0) {
    UINT virtKey = ((wParam >> 16) & 0xffu);
    if ((state & KeyModifierShift) != 0 && (virtKey == VK_LSHIFT || virtKey == VK_RSHIFT)) {
      result = true;
    }
    if ((state & KeyModifierControl) != 0 && (virtKey == VK_LCONTROL || virtKey == VK_RCONTROL)) {
      result = true;
    }
    if ((state & KeyModifierAlt) != 0 && (virtKey == VK_LMENU || virtKey == VK_RMENU)) {
      result = true;
    }
    if ((state & KeyModifierSuper) != 0 && (virtKey == VK_LWIN || virtKey == VK_RWIN)) {
      result = true;
    }
  }

  return result;
}
