@echo off
set PATH=C:\Program Files\Go\bin;%PATH%
go build -ldflags="-H windowsgui" -o enkoder.exe .
if errorlevel 1 exit /b 1
echo Built enkoder.exe (no console window)
