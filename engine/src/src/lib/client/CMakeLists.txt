# SPDX-FileCopyrightText: (C) 2024, 2026 Chris Rizzitello <sithlord48@gmail.com>
# SPDX-FileCopyrightText: (C) 2012 - 2024 Symless Ltd
# SPDX-FileCopyrightText: (C) 2009 - 2012 Nick Bolton
# SPDX-License-Identifier: MIT

add_library(client STATIC
  Client.cpp
  Client.h
  ServerProxy.cpp
  ServerProxy.h
)

target_link_libraries(client PUBLIC common)

if(UNIX)
  # Client.cpp uses app-lib symbols (FileReceiver, DragInformation, ...); GNU ld
  # is archive-order sensitive so the dependency must be explicit (macOS ld64
  # resolves it regardless, which is why this only bit the Linux build).
  target_link_libraries(client PUBLIC app)
endif()
