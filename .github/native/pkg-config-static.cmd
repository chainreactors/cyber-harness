@echo off
setlocal
set "MINGW_PKG_CONFIG="
for /f "delims=" %%I in ('where gcc.exe 2^>nul') do (
  set "MINGW_PKG_CONFIG=%%~dpIpkg-config.exe"
  goto found
)
:found
if not defined MINGW_PKG_CONFIG goto fallback
if not exist "%MINGW_PKG_CONFIG%" goto fallback
"%MINGW_PKG_CONFIG%" --static %*
exit /b %ERRORLEVEL%

:fallback
pkg-config --static %*
