# DeskBridge Input

The input engine lives in `engine/src` as part of the DeskBridge monorepo. It is
derived from Deskflow and retains the imported revision, copyright notices and
licenses. The embedded build omits the standalone GUI. Build requirements:
CMake, Ninja, Qt 6.7+ development libraries, OpenSSL, and platform development
dependencies.

Run `node scripts/build-input.cjs` from the repository root after installing the
desktop dependencies. The script builds the in-repository headless target and,
on macOS, uses macdeployqt to embed Qt frameworks
in `work/embedded-input/DeskBridge Input.app`. It includes the engine's source
tree and notices and verifies an ad-hoc code signature. This is a local test
bundle, not an Apple-notarized release. Set QT_PREFIX to select a Qt installation
other than Homebrew qtbase.

The macOS bundle has been built and its version command executed successfully.
The desktop resolver supports this nested bundle, an explicit
DESKBRIDGE_INPUT_BIN override, and the existing installed engine as a migration
fallback. It does not switch the running service simply by building the bundle.

macOS input authorization, full desktop distribution packaging, bundled
third-party notices, and physical Linux interoperability still need validation.
Keep the existing working engine until these checks pass. Linux compilation
produces a binary only; runtime dependency packaging is not implemented yet.

Distributions must include the engine's corresponding source and applicable
license notices. The binary is not an independently authored input engine.
