@echo off
setlocal EnableExtensions EnableDelayedExpansion

REM YukiHub Windows release builder for Wails v3.
REM Usage: scripts\build.bat [portable|installer|installer-payload|installer-package|all] [version] [amd64|arm64]

cd /d "%~dp0\.."

set "BUILD_MODE=%~1"
if not defined BUILD_MODE set "BUILD_MODE=all"
set "VERSION_ARG=%~2"
set "TARGET_ARCH=%~3"

if /i "%VERSION_ARG%"=="amd64" (
    set "TARGET_ARCH=amd64"
    set "VERSION_ARG="
)
if /i "%VERSION_ARG%"=="x64" (
    set "TARGET_ARCH=amd64"
    set "VERSION_ARG="
)
if /i "%VERSION_ARG%"=="arm64" (
    set "TARGET_ARCH=arm64"
    set "VERSION_ARG="
)
if /i "%VERSION_ARG%"=="aarch64" (
    set "TARGET_ARCH=arm64"
    set "VERSION_ARG="
)

if not defined TARGET_ARCH set "TARGET_ARCH=amd64"
if /i "%TARGET_ARCH%"=="x64" set "TARGET_ARCH=amd64"
if /i "%TARGET_ARCH%"=="aarch64" set "TARGET_ARCH=arm64"

if /i not "%BUILD_MODE%"=="portable" if /i not "%BUILD_MODE%"=="installer" if /i not "%BUILD_MODE%"=="installer-payload" if /i not "%BUILD_MODE%"=="installer-package" if /i not "%BUILD_MODE%"=="all" goto :usage
if /i not "%TARGET_ARCH%"=="amd64" if /i not "%TARGET_ARCH%"=="arm64" goto :usage

call :load_build_env
if errorlevel 1 goto :build_failed
call :resolve_version
if errorlevel 1 goto :build_failed
call :configure_architecture
if errorlevel 1 goto :build_failed
call :configure_ldflags
if errorlevel 1 goto :build_failed
call :print_build_info

if /i "%BUILD_MODE%"=="installer-package" goto :dispatch

call :check_build_tools
if errorlevel 1 goto :build_failed
call :prepare_release_build
if errorlevel 1 goto :build_failed

:dispatch
if /i "%BUILD_MODE%"=="portable" (
    call :build_portable
    if errorlevel 1 goto :build_failed
    goto :done
)
if /i "%BUILD_MODE%"=="installer-payload" (
    call :build_installer_payload
    if errorlevel 1 goto :build_failed
    goto :done
)
if /i "%BUILD_MODE%"=="installer-package" (
    where wails3 >nul 2>nul || (echo ERROR: wails3 was not found in PATH.& goto :build_failed)
    where makensis >nul 2>nul || (echo ERROR: makensis was not found in PATH. Install NSIS first.& goto :build_failed)
    call :build_installer_package
    if errorlevel 1 goto :build_failed
    goto :done
)
if /i "%BUILD_MODE%"=="installer" (
    call :build_installer
    if errorlevel 1 goto :build_failed
    goto :done
)
if /i "%BUILD_MODE%"=="all" (
    call :build_portable
    if errorlevel 1 goto :build_failed
    call :build_installer
    if errorlevel 1 goto :build_failed
    goto :done
)

:load_build_env
set "BUILD_ENV_FILE="
if exist ".env.build" set "BUILD_ENV_FILE=.env.build"
if not defined BUILD_ENV_FILE if exist ".env" set "BUILD_ENV_FILE=.env"

if defined BUILD_ENV_FILE (
    for /f "usebackq tokens=1,* delims==" %%A in ("!BUILD_ENV_FILE!") do (
        if /i "%%A"=="YUKIHUB_BANGUMI_CLIENT_ID" if not defined YUKIHUB_BANGUMI_CLIENT_ID set "YUKIHUB_BANGUMI_CLIENT_ID=%%B"
        if /i "%%A"=="YUKIHUB_BANGUMI_CLIENT_SECRET" if not defined YUKIHUB_BANGUMI_CLIENT_SECRET set "YUKIHUB_BANGUMI_CLIENT_SECRET=%%B"
        if /i "%%A"=="YUKIHUB_HIKARINAGI_CLIENT_ID" if not defined YUKIHUB_HIKARINAGI_CLIENT_ID set "YUKIHUB_HIKARINAGI_CLIENT_ID=%%B"
        if /i "%%A"=="YUKIHUB_HIKARINAGI_CLIENT_SECRET" if not defined YUKIHUB_HIKARINAGI_CLIENT_SECRET set "YUKIHUB_HIKARINAGI_CLIENT_SECRET=%%B"
        if /i "%%A"=="YUKIHUB_TOUCHGAL_TOKEN" if not defined YUKIHUB_TOUCHGAL_TOKEN set "YUKIHUB_TOUCHGAL_TOKEN=%%B"
        if /i "%%A"=="YUKIHUB_UMBRA_CLIENT_ID" if not defined YUKIHUB_UMBRA_CLIENT_ID set "YUKIHUB_UMBRA_CLIENT_ID=%%B"
        if /i "%%A"=="YUKIHUB_UMBRA_REGISTRATION_TOKEN" if not defined YUKIHUB_UMBRA_REGISTRATION_TOKEN set "YUKIHUB_UMBRA_REGISTRATION_TOKEN=%%B"
    )
)
exit /b 0

:resolve_version
if defined VERSION_ARG (
    set "VERSION=%VERSION_ARG%"
) else (
    set "VERSION="
    for /f "delims=" %%i in ('git describe --tags --abbrev=0 --match "v[0-9]*" 2^>nul') do if not defined VERSION set "VERSION=%%i"
    if not defined VERSION set "VERSION=v1.0.0"
)
if "!VERSION:~0,1!"=="v" set "VERSION=!VERSION:~1!"

set "GIT_COMMIT="
for /f "delims=" %%i in ('git rev-parse --short HEAD 2^>nul') do if not defined GIT_COMMIT set "GIT_COMMIT=%%i"
if not defined GIT_COMMIT set "GIT_COMMIT=unknown"

set "BUILD_TIME="
for /f "tokens=*" %%i in ('powershell -NoProfile -Command "Get-Date -Format 'yyyy-MM-dd HH:mm:ss'"') do set "BUILD_TIME=%%i"
if not defined BUILD_TIME set "BUILD_TIME=unknown"
exit /b 0

:configure_architecture
set "GOOS=windows"
set "GOARCH=%TARGET_ARCH%"
set "CGO_ENABLED=1"
set "GO_BUILD_TAGS=production"
set "DUCKDB_DLL="
set "DUCKDB_BUILD_LIB_DIR="
set "TOOLCHAIN_BIN="
set "SEVENZIP_SOURCE_DIR=%CD%\lib\win%TARGET_ARCH%\7z"
set "SEVENZIP_BUILD_DIR=%CD%\build\bin\7z"
set "WINDOWS_PAYLOAD_DIR=%CD%\build\windows\payload\%TARGET_ARCH%"

if not exist "!SEVENZIP_SOURCE_DIR!\7z.exe" (
    echo ERROR: Missing !SEVENZIP_SOURCE_DIR!\7z.exe
    exit /b 1
)
if not exist "!SEVENZIP_SOURCE_DIR!\7z.dll" (
    echo ERROR: Missing !SEVENZIP_SOURCE_DIR!\7z.dll
    exit /b 1
)

if /i "%TARGET_ARCH%"=="arm64" (
    set "DUCKDB_SOURCE_LIB_DIR=%CD%\lib\winarm64"
    set "DUCKDB_BUILD_LIB_DIR=%CD%\build\duckdb\winarm64"
    set "ARM64_TARGET_TRIPLE=aarch64-w64-windows-gnu"

    if not exist "!DUCKDB_SOURCE_LIB_DIR!\duckdb.dll" (
        echo ERROR: Missing !DUCKDB_SOURCE_LIB_DIR!\duckdb.dll
        exit /b 1
    )
    if not exist "!DUCKDB_SOURCE_LIB_DIR!\duckdb.lib" (
        echo ERROR: Missing !DUCKDB_SOURCE_LIB_DIR!\duckdb.lib
        exit /b 1
    )
    if not exist "!DUCKDB_BUILD_LIB_DIR!" mkdir "!DUCKDB_BUILD_LIB_DIR!"
    copy /Y "!DUCKDB_SOURCE_LIB_DIR!\duckdb.dll" "!DUCKDB_BUILD_LIB_DIR!\duckdb.dll" >nul
    if errorlevel 1 exit /b 1
    copy /Y "!DUCKDB_SOURCE_LIB_DIR!\duckdb.lib" "!DUCKDB_BUILD_LIB_DIR!\libduckdb.dll.a" >nul
    if errorlevel 1 exit /b 1

    if defined MSYS2_LOCATION if exist "!MSYS2_LOCATION!\clangarm64\bin\clang.exe" set "TOOLCHAIN_BIN=!MSYS2_LOCATION!\clangarm64\bin"
    if not defined TOOLCHAIN_BIN if exist "C:\msys64\clangarm64\bin\clang.exe" set "TOOLCHAIN_BIN=C:\msys64\clangarm64\bin"
    if defined TOOLCHAIN_BIN (
        set "CC=!TOOLCHAIN_BIN!\clang.exe --target=!ARM64_TARGET_TRIPLE!"
        if exist "!TOOLCHAIN_BIN!\clang++.exe" set "CXX=!TOOLCHAIN_BIN!\clang++.exe --target=!ARM64_TARGET_TRIPLE!"
    ) else if not defined CC (
        where aarch64-w64-mingw32-gcc >nul 2>nul
        if not errorlevel 1 set "CC=aarch64-w64-mingw32-gcc"
    )

    set "CGO_LDFLAGS=-L!DUCKDB_BUILD_LIB_DIR! -lduckdb"
    set "GO_BUILD_TAGS=production,duckdb_use_lib"
    set "DUCKDB_DLL=!DUCKDB_BUILD_LIB_DIR!\duckdb.dll"
)

if not defined CC (
    where gcc >nul 2>nul
    if not errorlevel 1 set "CC=gcc"
)
if not defined CC (
    echo ERROR: Windows %TARGET_ARCH% CGO build requires a matching C compiler.
    echo        Install MSYS2 and its %TARGET_ARCH% toolchain, or set CC explicitly.
    exit /b 1
)

set "CC_TARGET="
for /f "delims=" %%i in ('!CC! -dumpmachine 2^>nul') do if not defined CC_TARGET set "CC_TARGET=%%i"
if not defined CC_TARGET (
    echo ERROR: Failed to inspect C compiler target: !CC!
    exit /b 1
)
if /i "%TARGET_ARCH%"=="amd64" (
    echo !CC_TARGET! | findstr /i "x86_64 amd64" >nul
) else (
    echo !CC_TARGET! | findstr /i "aarch64 arm64" >nul
)
if errorlevel 1 (
    echo ERROR: C compiler target !CC_TARGET! does not match %TARGET_ARCH%.
    echo        CC=!CC!
    exit /b 1
)

if defined TOOLCHAIN_BIN set "PATH=!TOOLCHAIN_BIN!;!PATH!"
if defined DUCKDB_BUILD_LIB_DIR set "PATH=!DUCKDB_BUILD_LIB_DIR!;!PATH!"
exit /b 0

:configure_ldflags
set "LDFLAGS_BANGUMI="
set "LDFLAGS_HIKARINAGI="
set "LDFLAGS_TOUCHGAL="
set "LDFLAGS_UMBRA="
set "LDFLAGS_UPDATE_SERVICE="
set "BANGUMI_OAUTH_STATUS=disabled"
set "HIKARINAGI_OAUTH_STATUS=disabled"
set "TOUCHGAL_TOKEN_STATUS=disabled"
set "UMBRA_REGISTRATION_STATUS=disabled"

if defined YUKIHUB_UPDATE_SERVICE_URL (
    set "LDFLAGS_UPDATE_SERVICE= -X 'yukihub/internal/version.UpdateServiceURL=!YUKIHUB_UPDATE_SERVICE_URL!'"
)

if defined YUKIHUB_BANGUMI_CLIENT_ID (
    if not defined YUKIHUB_BANGUMI_CLIENT_SECRET (
        echo ERROR: YUKIHUB_BANGUMI_CLIENT_ID and YUKIHUB_BANGUMI_CLIENT_SECRET must be configured together.
        exit /b 1
    )
    set "LDFLAGS_BANGUMI= -X 'yukihub/internal/version.BangumiOAuthClientID=!YUKIHUB_BANGUMI_CLIENT_ID!' -X 'yukihub/internal/version.BangumiOAuthClientSecret=!YUKIHUB_BANGUMI_CLIENT_SECRET!'"
    set "BANGUMI_OAUTH_STATUS=enabled"
) else if defined YUKIHUB_BANGUMI_CLIENT_SECRET (
    echo ERROR: YUKIHUB_BANGUMI_CLIENT_ID and YUKIHUB_BANGUMI_CLIENT_SECRET must be configured together.
    exit /b 1
)

if defined YUKIHUB_HIKARINAGI_CLIENT_ID (
    set "LDFLAGS_HIKARINAGI= -X 'yukihub/internal/version.HikarinagiOAuthClientID=!YUKIHUB_HIKARINAGI_CLIENT_ID!'"
    if defined YUKIHUB_HIKARINAGI_CLIENT_SECRET (
        set "LDFLAGS_HIKARINAGI=!LDFLAGS_HIKARINAGI! -X 'yukihub/internal/version.HikarinagiOAuthClientSecret=!YUKIHUB_HIKARINAGI_CLIENT_SECRET!'"
    )
    set "HIKARINAGI_OAUTH_STATUS=enabled"
) else if defined YUKIHUB_HIKARINAGI_CLIENT_SECRET (
    echo ERROR: YUKIHUB_HIKARINAGI_CLIENT_SECRET requires YUKIHUB_HIKARINAGI_CLIENT_ID.
    exit /b 1
)

if defined YUKIHUB_TOUCHGAL_TOKEN (
    set "LDFLAGS_TOUCHGAL= -X 'yukihub/internal/version.TouchGalAPIToken=!YUKIHUB_TOUCHGAL_TOKEN!'"
    set "TOUCHGAL_TOKEN_STATUS=enabled"
)

if defined YUKIHUB_UMBRA_CLIENT_ID (
    if not defined YUKIHUB_UMBRA_REGISTRATION_TOKEN (
        echo ERROR: YUKIHUB_UMBRA_CLIENT_ID and YUKIHUB_UMBRA_REGISTRATION_TOKEN must be configured together.
        exit /b 1
    )
    set "LDFLAGS_UMBRA= -X 'yukihub/internal/version.UmbraOAuthClientID=!YUKIHUB_UMBRA_CLIENT_ID!' -X 'yukihub/internal/version.UmbraRegistrationToken=!YUKIHUB_UMBRA_REGISTRATION_TOKEN!'"
    set "UMBRA_REGISTRATION_STATUS=enabled"
) else if defined YUKIHUB_UMBRA_REGISTRATION_TOKEN (
    echo ERROR: YUKIHUB_UMBRA_CLIENT_ID and YUKIHUB_UMBRA_REGISTRATION_TOKEN must be configured together.
    exit /b 1
)

set "LDFLAGS_BASE=-s -w -X 'yukihub/internal/version.Version=%VERSION%' -X 'yukihub/internal/version.GitCommit=%GIT_COMMIT%' -X 'yukihub/internal/version.BuildTime=%BUILD_TIME%'!LDFLAGS_UPDATE_SERVICE!!LDFLAGS_BANGUMI!!LDFLAGS_HIKARINAGI!!LDFLAGS_TOUCHGAL!!LDFLAGS_UMBRA!"
set "LDFLAGS_PORTABLE=!LDFLAGS_BASE! -X 'yukihub/internal/version.BuildMode=portable'"
set "LDFLAGS_INSTALLER=!LDFLAGS_BASE! -X 'yukihub/internal/version.BuildMode=installer'"
set "LDFLAGS_GUI_PORTABLE=!LDFLAGS_PORTABLE! -H windowsgui"
set "LDFLAGS_GUI_INSTALLER=!LDFLAGS_INSTALLER! -H windowsgui"
exit /b 0

:print_build_info
echo ========================================
echo YukiHub Wails v3 Windows Build
echo Build Mode: %BUILD_MODE%
echo Target: windows/%TARGET_ARCH%
echo Version: %VERSION%
echo Commit: %GIT_COMMIT%
echo CGO Compiler: !CC! ^(!CC_TARGET!^)
if defined BUILD_ENV_FILE echo Build Env File: !BUILD_ENV_FILE!
echo Bangumi OAuth Injection: !BANGUMI_OAUTH_STATUS!
echo Hikarinagi OAuth Injection: !HIKARINAGI_OAUTH_STATUS!
echo TouchGAL Token Injection: !TOUCHGAL_TOKEN_STATUS!
echo Umbra Registration Token Injection: !UMBRA_REGISTRATION_STATUS!
if defined DUCKDB_DLL echo DuckDB Dynamic DLL: !DUCKDB_DLL!
echo Bundled 7z: !SEVENZIP_SOURCE_DIR!
echo ========================================
echo.
exit /b 0

:check_build_tools
where go >nul 2>nul || (echo ERROR: go was not found in PATH.& exit /b 1)
where pnpm >nul 2>nul || (echo ERROR: pnpm was not found in PATH.& exit /b 1)
where wails3 >nul 2>nul
if errorlevel 1 (
    set "GO_BIN_DIR="
    for /f "delims=" %%i in ('go env GOPATH 2^>nul') do if not defined GO_BIN_DIR set "GO_BIN_DIR=%%i\bin"
    if defined GO_BIN_DIR if exist "!GO_BIN_DIR!\wails3.exe" set "PATH=!GO_BIN_DIR!;!PATH!"
)
where wails3 >nul 2>nul || (echo ERROR: wails3 was not found in PATH.& exit /b 1)

set "EXPECTED_WAILS_VERSION="
for /f "delims=" %%i in ('go list -m -f "{{.Version}}" github.com/wailsapp/wails/v3 2^>nul') do if not defined EXPECTED_WAILS_VERSION set "EXPECTED_WAILS_VERSION=%%i"
set "ACTUAL_WAILS_VERSION="
for /f "delims=" %%i in ('wails3 version 2^>^&1') do if not defined ACTUAL_WAILS_VERSION set "ACTUAL_WAILS_VERSION=%%i"
if not defined EXPECTED_WAILS_VERSION (
    echo ERROR: Unable to resolve the Wails v3 version from go.mod.
    exit /b 1
)
if /i not "!EXPECTED_WAILS_VERSION!"=="!ACTUAL_WAILS_VERSION!" (
    echo ERROR: wails3 version mismatch. Expected !EXPECTED_WAILS_VERSION!, found !ACTUAL_WAILS_VERSION!.
    echo        Run: go install github.com/wailsapp/wails/v3/cmd/wails3@!EXPECTED_WAILS_VERSION!
    exit /b 1
)
exit /b 0

:prepare_release_build
if not exist "build\bin" mkdir "build\bin"

echo [prepare] Installing locked frontend dependencies...
call pnpm --dir frontend install --frozen-lockfile
if errorlevel 1 exit /b 1

echo [prepare] Generating Wails v3 bindings...
wails3 generate bindings -clean=true -ts
if errorlevel 1 exit /b 1

echo [prepare] Building production frontend...
call pnpm --dir frontend build
if errorlevel 1 exit /b 1
echo.
exit /b 0

:generate_windows_resources
set "WAILS_SYSO=wails_windows_%TARGET_ARCH%.syso"
if exist "!WAILS_SYSO!" del /q "!WAILS_SYSO!"
wails3 generate syso -arch %TARGET_ARCH% -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info build/windows/info.json -out "!WAILS_SYSO!"
if errorlevel 1 exit /b 1
exit /b 0

:build_gui
call :generate_windows_resources
if errorlevel 1 exit /b 1
powershell -NoProfile -Command "$ErrorActionPreference = 'Stop'; & go build -tags $env:GO_BUILD_TAGS -trimpath -buildvcs=false -ldflags $env:YUKIHUB_GO_LDFLAGS -o $env:YUKIHUB_GO_OUTPUT .; exit $LASTEXITCODE"
set "GUI_BUILD_EXIT=!ERRORLEVEL!"
if exist "!WAILS_SYSO!" del /q "!WAILS_SYSO!"
if not "!GUI_BUILD_EXIT!"=="0" exit /b !GUI_BUILD_EXIT!
exit /b 0

:build_portable
echo [portable 1/3] Building GUI...
set "PORTABLE_GUI=build\bin\yukihub-%TARGET_ARCH%-portable.exe"
set "YUKIHUB_GO_OUTPUT=!PORTABLE_GUI!"
set "YUKIHUB_GO_LDFLAGS=!LDFLAGS_GUI_PORTABLE!"
call :build_gui
if errorlevel 1 exit /b 1

echo [portable 2/3] Building standalone updater...
call :build_updater
if errorlevel 1 exit /b 1

echo [portable 3/3] Creating ZIP...
set "PORTABLE_DIR=build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-portable"
set "PORTABLE_ZIP=!PORTABLE_DIR!.zip"
if exist "!PORTABLE_DIR!" rmdir /s /q "!PORTABLE_DIR!"
mkdir "!PORTABLE_DIR!"
mkdir "!PORTABLE_DIR!\backups"
mkdir "!PORTABLE_DIR!\covers"
mkdir "!PORTABLE_DIR!\backgrounds"
mkdir "!PORTABLE_DIR!\logs"
copy /Y "!PORTABLE_GUI!" "!PORTABLE_DIR!\YukiHub.exe" >nul
copy /Y "build\bin\YukiHubUpdater.exe" "!PORTABLE_DIR!\YukiHubUpdater.exe" >nul
if defined DUCKDB_DLL copy /Y "!DUCKDB_DLL!" "!PORTABLE_DIR!\duckdb.dll" >nul
mkdir "!PORTABLE_DIR!\7z"
copy /Y "!SEVENZIP_SOURCE_DIR!\7z.exe" "!PORTABLE_DIR!\7z\7z.exe" >nul
copy /Y "!SEVENZIP_SOURCE_DIR!\7z.dll" "!PORTABLE_DIR!\7z\7z.dll" >nul
REM Bundled locale switcher / upscaler tools (third-party programs, redistributed
REM as-is, see compat-tools\SOURCE.txt)
if exist "build\compat-tools" (
    xcopy /E /I /Y "build\compat-tools" "!PORTABLE_DIR!\compat-tools" >nul
)

>"!PORTABLE_DIR!\README.txt" echo YukiHub Portable v%VERSION%
>>"!PORTABLE_DIR!\README.txt" echo.
>>"!PORTABLE_DIR!\README.txt" echo This package contains:
>>"!PORTABLE_DIR!\README.txt" echo   - YukiHub.exe  : GUI version ^(double-click to launch^)
>>"!PORTABLE_DIR!\README.txt" echo   - YukiHubUpdater.exe : standalone update helper

if exist "!PORTABLE_ZIP!" del /q "!PORTABLE_ZIP!"
powershell -NoProfile -Command "Compress-Archive -LiteralPath '!PORTABLE_DIR!' -DestinationPath '!PORTABLE_ZIP!' -CompressionLevel Optimal"
if errorlevel 1 exit /b 1
rmdir /s /q "!PORTABLE_DIR!"
echo Created: !PORTABLE_ZIP!
echo.
exit /b 0

:build_updater
powershell -NoProfile -Command "$ErrorActionPreference = 'Stop'; & go -C updater build -trimpath -buildvcs=false -ldflags '-s -w -H=windowsgui' -o '..\build\bin\YukiHubUpdater.exe' ./cmd/yukihub-updater; exit $LASTEXITCODE"
if errorlevel 1 exit /b 1
exit /b 0

:prepare_installer_runtime
if not exist "build\bin" mkdir "build\bin"
if exist "build\bin\duckdb.dll" del /q "build\bin\duckdb.dll"
if defined DUCKDB_DLL copy /Y "!DUCKDB_DLL!" "build\bin\duckdb.dll" >nul
if exist "!SEVENZIP_BUILD_DIR!" rmdir /s /q "!SEVENZIP_BUILD_DIR!"
mkdir "!SEVENZIP_BUILD_DIR!"
copy /Y "!SEVENZIP_SOURCE_DIR!\7z.exe" "!SEVENZIP_BUILD_DIR!\7z.exe" >nul
copy /Y "!SEVENZIP_SOURCE_DIR!\7z.dll" "!SEVENZIP_BUILD_DIR!\7z.dll" >nul

REM Bundled locale switcher / upscaler tools (third-party programs, redistributed
REM as-is, see compat-tools\SOURCE.txt)
if exist "%CD%\build\compat-tools" (
    if exist "%WINDOWS_PAYLOAD_DIR%\compat-tools" rmdir /s /q "%WINDOWS_PAYLOAD_DIR%\compat-tools"
    xcopy /E /I /Y "%CD%\build\compat-tools" "%WINDOWS_PAYLOAD_DIR%\compat-tools" >nul
)

set "WEBVIEW2_GEN_DIR=%CD%\build\windows\webview2bootstrapper"
wails3 generate webview2bootstrapper -dir "!WEBVIEW2_GEN_DIR!"
if errorlevel 1 exit /b 1
if not exist "!WEBVIEW2_GEN_DIR!\MicrosoftEdgeWebview2Setup.exe" (
    echo ERROR: Wails v3 did not generate the WebView2 bootstrapper.
    exit /b 1
)
copy /Y "!WEBVIEW2_GEN_DIR!\MicrosoftEdgeWebview2Setup.exe" "build\windows\nsis\MicrosoftEdgeWebview2Setup.exe" >nul
if errorlevel 1 exit /b 1
exit /b 0

:build_installer
where wails3 >nul 2>nul || (echo ERROR: wails3 was not found in PATH.& exit /b 1)
where makensis >nul 2>nul || (echo ERROR: makensis was not found in PATH. Install NSIS first.& exit /b 1)
call :build_installer_payload
if errorlevel 1 exit /b 1
call :build_installer_package
if errorlevel 1 exit /b 1
if exist "build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-installer-payload.zip" del /q "build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-installer-payload.zip"
exit /b 0

:build_installer_payload
if not exist "!WINDOWS_PAYLOAD_DIR!" mkdir "!WINDOWS_PAYLOAD_DIR!"
echo [installer payload 1/3] Building GUI...
set "YUKIHUB_GO_OUTPUT=!WINDOWS_PAYLOAD_DIR!\YukiHub.exe"
set "YUKIHUB_GO_LDFLAGS=!LDFLAGS_GUI_INSTALLER!"
call :build_gui
if errorlevel 1 exit /b 1
call :prepare_installer_runtime
if errorlevel 1 exit /b 1

echo [installer payload 2/3] Building standalone updater...
call :build_updater
if errorlevel 1 exit /b 1

echo [installer payload 3/3] Creating signing payload...
set "INSTALLER_PAYLOAD_ZIP=build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-installer-payload.zip"
if exist "!INSTALLER_PAYLOAD_ZIP!" del /q "!INSTALLER_PAYLOAD_ZIP!"
powershell -NoProfile -Command "$Files = (Join-Path $env:WINDOWS_PAYLOAD_DIR 'YukiHub.exe'), (Join-Path $PWD 'build\bin\YukiHubUpdater.exe'); Compress-Archive -LiteralPath $Files -DestinationPath '!INSTALLER_PAYLOAD_ZIP!' -CompressionLevel Optimal"
if errorlevel 1 exit /b 1
echo Created: !INSTALLER_PAYLOAD_ZIP!
echo.
exit /b 0

:build_installer_package
if not exist "!WINDOWS_PAYLOAD_DIR!\YukiHub.exe" (
    echo ERROR: Missing installer GUI payload: !WINDOWS_PAYLOAD_DIR!\YukiHub.exe
    exit /b 1
)
REM The yukihubcli command line tool was removed as a product decision, so the
REM installer payload now contains only the GUI and the standalone updater.
if not exist "build\bin\YukiHubUpdater.exe" (
    echo ERROR: Missing standalone updater: build\bin\YukiHubUpdater.exe
    exit /b 1
)
call :prepare_installer_runtime
if errorlevel 1 exit /b 1

echo [installer package] Building NSIS installer...
set "WAILS_BINARY_DEFINE=ARG_WAILS_AMD64_BINARY=..\payload\amd64\YukiHub.exe"
if /i "%TARGET_ARCH%"=="arm64" set "WAILS_BINARY_DEFINE=ARG_WAILS_ARM64_BINARY=..\payload\arm64\YukiHub.exe"

pushd "build\windows\nsis"
makensis /D!WAILS_BINARY_DEFINE! project.nsi
set "MAKENSIS_EXIT=!ERRORLEVEL!"
popd
if not "!MAKENSIS_EXIT!"=="0" exit /b !MAKENSIS_EXIT!

set "INSTALLER_OUTPUT=build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-setup.exe"
if exist "!INSTALLER_OUTPUT!" del /q "!INSTALLER_OUTPUT!"
if exist "build\bin\YukiHub-%TARGET_ARCH%-installer.exe" (
    move /Y "build\bin\YukiHub-%TARGET_ARCH%-installer.exe" "!INSTALLER_OUTPUT!" >nul
) else if exist "build\bin\yukihub-%TARGET_ARCH%-installer.exe" (
    move /Y "build\bin\yukihub-%TARGET_ARCH%-installer.exe" "!INSTALLER_OUTPUT!" >nul
) else (
    echo ERROR: NSIS output was not found for %TARGET_ARCH%.
    exit /b 1
)
echo Created: !INSTALLER_OUTPUT!
echo.
exit /b 0

:done
echo ========================================
echo Build completed successfully.
if /i "%BUILD_MODE%"=="portable" echo Portable: build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-portable.zip
if /i "%BUILD_MODE%"=="installer" echo Installer: build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-setup.exe
if /i "%BUILD_MODE%"=="installer-payload" echo Payload: build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-installer-payload.zip
if /i "%BUILD_MODE%"=="installer-package" echo Installer: build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-setup.exe
if /i "%BUILD_MODE%"=="all" (
    echo Portable: build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-portable.zip
    echo Installer: build\bin\YukiHub-%VERSION%-windows-%TARGET_ARCH%-setup.exe
)
echo ========================================
endlocal
exit /b 0

:usage
echo Usage: scripts\build.bat [portable^|installer^|installer-payload^|installer-package^|all] [version] [amd64^|arm64]
endlocal
exit /b 1

:build_failed
if defined WAILS_SYSO if exist "!WAILS_SYSO!" del /q "!WAILS_SYSO!"
echo ERROR: YukiHub build failed.
endlocal
exit /b 1
