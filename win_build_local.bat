@echo off
:: Set Go environment
go env -w GOOS=windows

:: Build download
echo Building download...
cd download
go build -ldflags="-s -w" -o download.exe
if %errorlevel% neq 0 (
    echo download build failed!
    exit /b %errorlevel%
)
upx -9 download.exe
move /Y download.exe ..\ >nul
cd ..

:: Build file_info
echo Building file_info...
cd file_info
go build -ldflags="-s -w" -o fInfo.exe
if %errorlevel% neq 0 (
    echo file_info build failed!
    exit /b %errorlevel%
)
upx -9 fInfo.exe
move /Y fInfo.exe ..\ >nul
cd ..

echo All builds completed successfully!
pause