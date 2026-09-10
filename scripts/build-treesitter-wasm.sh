#!/usr/bin/env bash
# Builds internal/adapter/treesitter/wasm/keelage-ts.wasm: the tree-sitter
# runtime + TypeScript/TSX grammars + csrc/keelage_ts.c as ONE wasm32-wasi
# reactor module. No CGO, no emscripten, no dynamic linking.
#
# Inputs are pinned; bump them together with internal/adapter/treesitter/VERSION.
# Requires: clang (wasm32 target) + wasm-ld, npm, apt-get download, dpkg.
set -euo pipefail

TS_RUNTIME_VERSION="${TS_RUNTIME_VERSION:-0.25.1}"      # npm: tree-sitter (vendors lib/src)
TS_TYPESCRIPT_VERSION="${TS_TYPESCRIPT_VERSION:-0.23.2}" # npm: tree-sitter-typescript
WASI_LIBC_PKG="${WASI_LIBC_PKG:-wasi-libc}"              # Ubuntu noble: 0.0~git20230113
WASM32_RT_PKG="${WASM32_RT_PKG:-libclang-rt-18-dev-wasm32}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/internal/adapter/treesitter/wasm/keelage-ts.wasm"
WORK="${WORK:-$(mktemp -d)}"
mkdir -p "$WORK"/{npm,src,sysroot,obj}
echo "work dir: $WORK"

# 1. sources
( cd "$WORK/npm" && npm pack --silent "tree-sitter@$TS_RUNTIME_VERSION" "tree-sitter-typescript@$TS_TYPESCRIPT_VERSION" )
mkdir -p "$WORK/src/runtime" "$WORK/src/grammar"
tar xzf "$WORK"/npm/tree-sitter-"$TS_RUNTIME_VERSION".tgz -C "$WORK/src/runtime" --strip-components=1
tar xzf "$WORK"/npm/tree-sitter-typescript-"$TS_TYPESCRIPT_VERSION".tgz -C "$WORK/src/grammar" --strip-components=1
LIB="$WORK/src/runtime/vendor/tree-sitter/lib"
GRAMMAR="$WORK/src/grammar"

# 2. sysroot (wasi-libc + compiler-rt builtins for wasm32)
( cd "$WORK/sysroot" && apt-get download "$WASI_LIBC_PKG" "$WASM32_RT_PKG" >/dev/null && for d in *.deb; do dpkg -x "$d" .; done )
SYSROOT="$(dirname "$(dirname "$(dirname "$(find "$WORK/sysroot" -name libc.a | head -1)")")")"
# clang looks for libclang_rt.builtins-wasm32.a under its resource dir; point it at the extracted one.
RESOURCE_DIR="$(dirname "$(dirname "$(dirname "$(find "$WORK/sysroot" -name 'libclang_rt.builtins-wasm32.a' | head -1)")")")"
echo "sysroot: $SYSROOT"
echo "resource dir: $RESOURCE_DIR"

CC=clang
CFLAGS=(--target=wasm32-wasi --sysroot="$SYSROOT" -O2 -std=gnu11 -fno-exceptions -fvisibility=hidden
        -D_WASI_EMULATED_PROCESS_CLOCKS -DNDEBUG
        -Wno-unused-but-set-variable -Wno-unused-parameter)

# 3. compile
$CC "${CFLAGS[@]}" -I"$LIB/include" -I"$LIB/src" -c "$LIB/src/lib.c" -o "$WORK/obj/lib.o"
for g in typescript tsx; do
  $CC "${CFLAGS[@]}" -I"$GRAMMAR/$g/src" -c "$GRAMMAR/$g/src/parser.c"  -o "$WORK/obj/$g-parser.o"
  $CC "${CFLAGS[@]}" -I"$GRAMMAR/$g/src" -c "$GRAMMAR/$g/src/scanner.c" -o "$WORK/obj/$g-scanner.o"
done
$CC "${CFLAGS[@]}" -I"$LIB/include" -c "$ROOT/internal/adapter/treesitter/csrc/keelage_ts.c" -o "$WORK/obj/keelage_ts.o"

# 4. link as a reactor (library) module: exports come from export_name attributes.
mkdir -p "$(dirname "$OUT")"
$CC --target=wasm32-wasi --sysroot="$SYSROOT" -resource-dir "$RESOURCE_DIR" -mexec-model=reactor -O2 \
  -Wl,--gc-sections -Wl,--strip-all -Wl,-z,stack-size=1048576 -Wl,--initial-memory=16777216 \
  "$WORK"/obj/*.o -lwasi-emulated-process-clocks -o "$OUT"
ls -la "$OUT"
echo "ok"
