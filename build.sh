#!/bin/bash

echo "Сборка File Upload Monitor для всех платформ..."

echo ""
echo "1. Сборка для macOS (.app без консоли)..."
mkdir -p FileUploadMonitor.app/Contents/MacOS
CGO_ENABLED=1 go build -ldflags="-s -w" -o FileUploadMonitor.app/Contents/MacOS/FileUploadMonitor main.go

if [ ! -f "FileUploadMonitor.app/Contents/Info.plist" ]; then
    cat > FileUploadMonitor.app/Contents/Info.plist << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>FileUploadMonitor</string>
	<key>CFBundleIdentifier</key>
	<string>com.fileuploadmonitor</string>
	<key>CFBundleName</key>
	<string>File Upload Monitor</string>
	<key>CFBundleVersion</key>
	<string>1.0</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>LSMinimumSystemVersion</key>
	<string>10.13</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>LSUIElement</key>
	<false/>
</dict>
</plist>
EOF
fi

echo "✓ macOS приложение готово: FileUploadMonitor.app"

echo ""
echo "2. Сборка для Windows (.exe без консоли)..."
GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui" -o file-upload-monitor.exe main.go

if [ $? -eq 0 ]; then
    echo "✓ Windows приложение готово: file-upload-monitor.exe"
else
    echo "✗ Ошибка при сборке для Windows"
fi

echo ""
echo "Готово!"
echo ""
echo "macOS: open FileUploadMonitor.app"
echo "Windows: file-upload-monitor.exe"
