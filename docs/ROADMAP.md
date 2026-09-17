# DeskBridge Roadmap — Windows + Çoklu Makine

Bu dosya, "yapılması gerekenler sırayla" listesidir. Her madde biter → kutu işaretlenir,
sürüm bump'lanır (`packaging/linux/control` tek kaynak), CI apt'ye publish eder.

Öncelik sırası yukarıdan aşağı. Bir track bitmeden bir sonrakine geçilmez (paralel not'lar hariç).

---

## Track 0 — Sürükle-bırak'ı kapat (DEVAM EDİYOR)

Mac↔Linux canlı cursor-carry drag. Transport + copy/paste zaten çalışıyor.

- [x] Transport (FileChunk / DragInformation / FileReceiver, bounded-memory)
- [x] macOS kaynak yakalama (OSXScreen pasteboard) + Linux XDND hedef (XWindowsScreen)
- [x] XDND stuck-drag watchdog (v0.3.6) — donma fix'i
- [x] Same-tick devir yarışı fix'i (v0.3.7)
- [ ] **0.3.7 canlı test** (iki taraf da 0.3.7): Mac'ten Nautilus'a dosya sürükle → düşür
- [ ] Linux→Mac yönünde de test (bidirectional)

---

## Track 1 — Windows'u NODE olarak ekle

İyi haber: **Deskflow motoru Windows platform katmanını zaten içeriyor**
(`engine/src/src/lib/platform/MSWindows*.{cpp,h}` — Screen, Hook, KeyState, Clipboard,
Desks, Watchdog hepsi var). Yani KVM/mouse/klavye Windows'ta zaten mümkün. Eksik olan:
build pipeline, paketleme, drag-drop (OLE) ve imza.

Sırayla:

- [ ] **1.1 Windows build (CI)** — `.github/workflows/windows-desktop.yml`:
      MSVC + CMake + Ninja, `DESKBRIDGE_ENGINE_ONLY=ON`, target `deskflow-core`.
      Çıktı: `deskbridge-input.exe`. (Linux CI'yi örnek al.)
- [ ] **1.2 Electron paketi (Windows)** — `desktop/` zaten cross-platform;
      `electron-builder` ile NSIS installer (`.exe`). Relay + engine'i embed et.
- [ ] **1.3 launchd karşılığı** — Windows'ta servis/otostart:
      Scheduled Task veya `HKCU\...\Run` (launchd plist yerine). `main.cjs`'e OS branch.
- [ ] **1.4 Drag-drop (OLE)** — `MSWindowsScreen` içine IDropSource/IDropTarget
      (XDND'nin Windows muadili). Kaynak: sürüklenen dosyayı yakala; hedef: gelen
      chunk'ları OLE drop ile bırak. (Kodlanmış taslak var, canlı test edilmedi.)
- [ ] **1.5 İzin/imza** — Windows'ta TCC yok ama UAC + SmartScreen var.
      Self-signed veya (ileride) Authenticode. Hook DLL için yönetici onayı akışı.
- [ ] **1.6 Windows↔Mac ve Windows↔Linux 2'li test** (hâlâ 2-peer relay ile).

> Track 1 sonunda: her ikili kombinasyon (win-mac, win-linux, mac-linux) çalışır.
> **3+ makine hâlâ olmaz** — o Track 2.

---

## Track 2 — Çoklu makine (3+): relay'i HUB'a çevir  ← asıl büyük iş

**Neden gerekli:** Şu anki relay **kesin 2-peer**. `cmd/deskbridge/relay.go` side'ı
`a` (Mac, TLS server) / `b` (Linux, TLS client) diye zorluyor; Cloudflare Durable
Object ikinci bir `a` veya `b` gelince **409 "already connected"** dönüyor
(`relay/worker.mjs`). Yani windows-mac-linux zinciri fiziksel olarak imkânsız — önce bu.

İyi haber: **Deskflow motoru N-ekranı zaten destekliyor** (1 server + N client, layout
config ile). Darboğaz sadece relay + pairing + keşif.

Sırayla:

- [ ] **2.1 Hub topolojisi (worker)** — Durable Object'i "1 hub (a) + N client"
      olacak şekilde genişlet: `a` tek, `b/c/d…` (veya `client-<id>`) çoklu slot.
      Hub, her client mesajını doğru client'a route eder. 409 kuralını sadece
      "aynı id" için tut.
- [ ] **2.2 Hub tarafı Go** — hub (side a) N ayrı yamux session açar (her client için
      bir). Deskflow server'ı N-screen config ile başlat. Client tarafı değişmez az.
- [ ] **2.3 Pairing çoklu-cihaz** — pairing kodu bir "oda"yı temsil etsin; her yeni
      cihaz aynı kodla katılır, benzersiz `deviceId` alır. (worker AUTH_HASH → room
      zaten var; cihaz kimliği ekle.)
- [ ] **2.4 Otomatik keşif/algılama** ("inen iki PC algılama tam") — bir cihaz online
      olunca hub'a kaydolur; desktop app'te canlı cihaz listesi (sağlık paneli zaten
      var, 0.3.3) otomatik güncellenir. Yeni cihaz → layout'a otomatik/sürükle ile eklenir.
- [ ] **2.5 Layout editörü** — hangi ekran nerede (sol/sağ/üst/alt). Desktop app'te
      tıkla-konumlandır zaten planlı; N-cihaza genişlet. Zincir = layout'ta sıralama.
- [ ] **2.6 3-makine testi** — windows-mac-linux; sonra 4-makine
      (windows-linux-mac-windows) — aynı odaya 4 cihaz.

---

## Track 3 — Uzaktan masaüstü (ekranı pencerede görme)  [ayrı büyük proje]

Araştırma + mimari planı ayrı yapıldı (MJPEG→H.264, yamux service id 3/4, WebCodecs
viewer, absolute-input). Track 2 bittikten sonra. Detay: bu repoda ayrı not.

---

## Notlar / kısıtlar

- Sürüm tek kaynak: `packaging/linux/control`. Bump → tag → CI apt publish.
- macOS imza stabil self-signed (`DESKBRIDGE_SIGN_IDENTITY`) — TCC grant'i korur.
- Güvenlik: tüm trafik mutual-TLS 1.3 tüneli içinde. Uzaktan kontrol/çoklu cihaz
  eklerken her yeni cihaz için açık pairing onayı şart (sessiz katılım yok).
