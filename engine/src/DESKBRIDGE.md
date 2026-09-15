# DeskBridge Input

This Deskflow-derived source is maintained inside the DeskBridge monorepo. It is
not an independently authored keyboard/mouse engine. The imported upstream
revision, copyright notices and licenses are preserved. The existing LICENSE and
LICENSES files govern this distribution.

The embedded build omits the standalone Deskflow GUI and produces
`deskbridge-input`, which is launched and configured by DeskBridge Desktop.
It retains the upstream protocol and settings format for migration compatibility.

```sh
cmake -S . -B build -G Ninja -DCMAKE_BUILD_TYPE=Release -DDESKBRIDGE_ENGINE_ONLY=ON
cmake --build build --target deskflow-core
```

The Qt development libraries remain build dependencies. Do not distribute an
engine binary without its corresponding source and third-party license notices.
macOS input permissions and physical Linux interoperability still require testing
for each packaged release.
