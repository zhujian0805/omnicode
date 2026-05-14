#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
build_dir="$repo_root/build"
binary_name="omnicode"
build_path="$build_dir/$binary_name"
install_dir="$HOME/.local/bin"
install_path="$install_dir/$binary_name"

mkdir -p "$build_dir"

echo "Building omnicode into $build_path"
(
  cd "$repo_root"
  go build -o "$build_path" ./cmd/omnicode
)

chmod +x "$build_path"

echo "Installing omnicode to $install_path"
mkdir -p "$install_dir"
cp "$build_path" "$install_path"
chmod +x "$install_path"

echo "Done"
echo "Build:   $build_path"
echo "Install: $install_path"
