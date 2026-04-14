@echo off
setlocal
cd /d "%~dp0"
set "LOG_FILE=%~dp0upgrade.log"

echo [%DATE% %TIME%] upgrade started >> "%LOG_FILE%" 2>&1

git rev-parse --abbrev-ref HEAD > "%TEMP%\ts_branch.txt" 2>&1
set /p CURRENT_BRANCH=<"%TEMP%\ts_branch.txt"
del "%TEMP%\ts_branch.txt" 2>nul

if /i not "%CURRENT_BRANCH%"=="golang" (
    echo [%DATE% %TIME%] upgrade failed: expected golang branch, got %CURRENT_BRANCH% >> "%LOG_FILE%"
    exit /b 1
)

echo [%DATE% %TIME%] pulling latest code >> "%LOG_FILE%" 2>&1
git pull --ff-only origin golang >> "%LOG_FILE%" 2>&1
if errorlevel 1 (
    echo [%DATE% %TIME%] upgrade failed: git pull failed >> "%LOG_FILE%"
    exit /b 1
)

echo [%DATE% %TIME%] building truthsocial.exe >> "%LOG_FILE%" 2>&1
go build -o truthsocial.exe.new . >> "%LOG_FILE%" 2>&1
if errorlevel 1 (
    echo [%DATE% %TIME%] upgrade failed: build failed >> "%LOG_FILE%"
    exit /b 1
)

move /y truthsocial.exe.new truthsocial.exe >> "%LOG_FILE%" 2>&1
if errorlevel 1 (
    echo [%DATE% %TIME%] upgrade failed: could not replace binary >> "%LOG_FILE%"
    exit /b 1
)

echo [%DATE% %TIME%] upgrade finished - please restart the application >> "%LOG_FILE%"
exit /b 0
