#!/usr/bin/env bash
# Compila piumy-gateway para todos los targets soportados a dist/.
# Corre en Git Bash / Linux / Mac. CGO_ENABLED=0 en todos (whatsmeow +
# modernc.org/sqlite son pure-Go, sin dependencia de un toolchain C por target).
set -e

mkdir -p dist

# VERSION (raíz del repo) es la única fuente del número de versión — nadie
# lo escribe a mano en otro lado. Dos consumidores no pueden leer ese
# archivo directo: go:embed no cruza el directorio del paquete (copia
# resincronizada acá) e Inno Setup no tiene un include-desde-archivo-plano
# cómodo (se genera un .iss con el #define, ver más abajo).
VERSION="$(tr -d '[:space:]' < VERSION)"
cp VERSION internal/version/VERSION
cat > installer/windows/version.iss <<EOF
; Generado por build-all.sh desde VERSION — no editar a mano.
#define MyAppVersion "$VERSION"
EOF

VERSION_LDFLAG="-X piumy-gateway/internal/version.Version=$VERSION"

build() {
	local goos=$1 goarch=$2 goarm=$3 ldflags=$4 name=$5 pkg=${6:-.}
	echo "building ${goos}/${goarch}${goarm:+v$goarm} (${pkg})..."
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
		go build -ldflags "$VERSION_LDFLAG $ldflags" -o "dist/$name" "$pkg"
}

build windows amd64 ""  "-H=windowsgui" piumy-gateway-windows-amd64.exe
build linux   amd64 ""  ""              piumy-gateway-linux-amd64
build linux   arm64 ""  ""              piumy-gateway-linux-arm64
build linux   arm   "7" ""              piumy-gateway-linux-armv7
build darwin  arm64 ""  ""              piumy-gateway-darwin-arm64
build darwin  amd64 ""  ""              piumy-gateway-darwin-amd64

ls -la dist/
