@echo off
setlocal EnableExtensions
rem Builds dist\ashgate-lite.exe with its administrator manifest and the
rem WinDivert driver next to it. Double-click to run.
rem Needs Go 1.25+ and, on the first run, internet access.

cd /d "%~dp0"

set "WINDIVERT_VERSION=2.2.2"
set "WINDIVERT_URL=https://github.com/basil00/WinDivert/releases/download/v%WINDIVERT_VERSION%/WinDivert-%WINDIVERT_VERSION%-A.zip"
set "GO_WINRES=github.com/tc-hib/go-winres@v0.3.1"
set "WD_TMP="

echo.
echo === Ashgate Lite build ===
echo.

rem ---------- 1. Go ----------
echo [1/5] Checking Go...
where go >nul 2>nul
if errorlevel 1 goto :no_go
for /f "delims=" %%v in ('go version') do echo       %%v

rem ---------- 2. dist folder ----------
echo [2/5] Preparing the dist folder...
if not exist "dist\" mkdir "dist"
if not exist "dist\" (
    echo ERROR: could not create the dist folder.
    goto :fail
)

rem ---------- 3. Administrator manifest ----------
echo [3/5] Generating the administrator manifest...
go run %GO_WINRES% make --in winres/winres.json --arch amd64 --out cmd/ashgate-lite/rsrc
if errorlevel 1 (
    echo ERROR: go-winres failed. Check your internet connection and try again.
    goto :fail
)

rem ---------- 4. Compile ----------
echo [4/5] Compiling dist\ashgate-lite.exe...
set "CGO_ENABLED=0"
set "GOOS=windows"
set "GOARCH=amd64"
go build -trimpath -ldflags "-s -w -H windowsgui" -o dist\ashgate-lite.exe .\cmd\ashgate-lite
if errorlevel 1 (
    echo ERROR: go build failed. If ashgate-lite.exe is running, close it and try again.
    goto :fail
)

rem ---------- 5. WinDivert driver ----------
echo [5/5] Checking the WinDivert driver...
if exist "dist\WinDivert.dll" if exist "dist\WinDivert64.sys" (
    echo       Already in dist, skipping the download.
    goto :done
)

set "WD_TMP=%TEMP%\ashgate-windivert-%RANDOM%%RANDOM%"
set "WD_ZIP=%WD_TMP%\WinDivert.zip"
mkdir "%WD_TMP%"
if errorlevel 1 (
    echo ERROR: could not create a temporary folder in %TEMP%.
    goto :fail
)

echo       Downloading WinDivert %WINDIVERT_VERSION%...
where curl >nul 2>nul
if errorlevel 1 goto :download_ps
curl -fL --retry 2 -o "%WD_ZIP%" "%WINDIVERT_URL%"
if not errorlevel 1 goto :extract
echo       curl failed, trying PowerShell...

:download_ps
powershell -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; [Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12; Invoke-WebRequest -UseBasicParsing -Uri $env:WINDIVERT_URL -OutFile $env:WD_ZIP"
if errorlevel 1 (
    echo ERROR: could not download %WINDIVERT_URL%
    goto :fail
)

:extract
echo       Extracting x64\WinDivert.dll and x64\WinDivert64.sys...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='Stop'; Expand-Archive -LiteralPath $env:WD_ZIP -DestinationPath $env:WD_TMP -Force"
if errorlevel 1 (
    echo ERROR: could not extract the WinDivert archive.
    goto :fail
)
set "WD_X64=%WD_TMP%\WinDivert-%WINDIVERT_VERSION%-A\x64"
copy /y "%WD_X64%\WinDivert.dll" "dist\" >nul
if errorlevel 1 (
    echo ERROR: WinDivert.dll not found in the archive.
    goto :fail
)
copy /y "%WD_X64%\WinDivert64.sys" "dist\" >nul
if errorlevel 1 (
    echo ERROR: WinDivert64.sys not found in the archive.
    goto :fail
)
rmdir /s /q "%WD_TMP%"
set "WD_TMP="

:done
echo.
echo BUILD SUCCEEDED
echo   dist\ashgate-lite.exe
echo   dist\WinDivert.dll
echo   dist\WinDivert64.sys
echo.
pause
exit /b 0

:no_go
echo.
echo ERROR: Go is not installed or is not on PATH.
echo Install Go 1.25 or newer from https://go.dev/dl/
echo then open a new window and run build.bat again.
goto :fail

:fail
if defined WD_TMP if exist "%WD_TMP%" rmdir /s /q "%WD_TMP%"
echo.
echo BUILD FAILED - see the message above.
echo.
pause
exit /b 1
