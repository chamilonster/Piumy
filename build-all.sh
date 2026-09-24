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

# --- Setup de Windows (Inno Setup) — ct-2026-09-23-2154 ----------------------
# Empaqueta dist/piumy-gateway-windows-amd64.exe (armado arriba) y lee el
# version.iss generado arriba. ISCC.exe es un programa de Windows: en
# Linux/Mac se avisa y se sigue — quien arma los otros 5 targets ahí no tiene
# un error que arreglar.
case "$(uname -s)" in
	MINGW* | MSYS* | CYGWIN*) ;;
	*)
		echo "NOTA: el setup de Windows (Inno Setup) solo se arma en Windows — omitido en $(uname -s)."
		ls -la dist/
		exit 0
		;;
esac

# ISCC.exe: en el PATH, o donde Inno Setup 6 se instala — para todos los
# usuarios (Program Files) o solo para uno (%LOCALAPPDATA%\Programs, lo que
# deja `winget install JRSoftware.InnoSetup` sin administrador).
iscc_dirs=(
	"$(printenv 'ProgramFiles(x86)' || true)/Inno Setup 6"
	"$(printenv PROGRAMFILES || true)/Inno Setup 6"
	"$(printenv LOCALAPPDATA || true)/Programs/Inno Setup 6"
)
iscc="$(command -v ISCC.exe || true)"
if [ -z "$iscc" ]; then
	for dir in "${iscc_dirs[@]}"; do
		if [ -f "$dir/ISCC.exe" ]; then
			iscc="$dir/ISCC.exe"
			break
		fi
	done
fi
if [ -z "$iscc" ]; then
	{
		echo "ERROR: no encontré ISCC.exe (Inno Setup 6) — el setup de Windows NO se armó."
		echo "Los binarios de dist/ sí quedaron armados. Busqué en:"
		echo "  - el PATH (ISCC.exe)"
		for dir in "${iscc_dirs[@]}"; do echo "  - $dir/ISCC.exe"; done
		echo "Instalalo (winget install JRSoftware.InnoSetup) o poné ISCC.exe en el PATH."
	} >&2
	exit 1
fi

echo "building setup de Windows con $iscc..."
# MSYS_NO_PATHCONV=1: Git Bash reescribe como ruta de Unix todo argumento que
# empieza con "/" — los flags de ISCC (/Q, /O...) llegan rotos, igual que el
# /c de `cmd.exe /c`. Hoy ISCC corre sin flags; queda puesto para el próximo
# que agregue uno.
MSYS_NO_PATHCONV=1 "$iscc" installer/windows/piumy.iss
setup="dist/Piumy-Setup-$VERSION.exe"
if [ ! -f "$setup" ]; then
	echo "ERROR: ISCC terminó sin error pero no existe $setup." >&2
	exit 1
fi

ls -la dist/
