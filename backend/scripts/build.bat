@echo off
REM Windows build: backend (Go) binaries only.
setlocal enabledelayedexpansion

cd /d "%~dp0\.."

if not exist bin mkdir bin
go build -trimpath -o bin\intelligent_customer.exe .\cmd\server
if errorlevel 1 goto :err
go build -trimpath -o bin\adminctl.exe .\cmd\adminctl
if errorlevel 1 goto :err

echo.
echo done.
echo   server: %CD%\bin\intelligent_customer.exe  (uses .env in cwd)
echo   cli:    %CD%\bin\adminctl.exe               -db %%DB_PATH%% ^<list^|add^|passwd^|rename^|del^>
goto :eof

:err
echo build failed.
exit /b 1
endlocal