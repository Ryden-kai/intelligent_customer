@echo off
cd /d "%~dp0\.."
if not exist .env (
  echo .env not found, copying from .env.example.
  copy /y .env.example .env >nul
)
bin\intelligent_customer.exe