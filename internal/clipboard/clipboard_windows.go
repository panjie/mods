//go:build windows

package clipboard

import (
	"fmt"
	"os"
	"strings"
)

func readImage() ([]byte, error) {
	script := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
if ([System.Windows.Forms.Clipboard]::ContainsImage()) {
    $img = [System.Windows.Forms.Clipboard]::GetImage()
    $file = [System.IO.Path]::GetTempPath() + [System.Guid]::NewGuid().ToString() + '.png'
    $img.Save($file, [System.Drawing.Imaging.ImageFormat]::Png)
    Write-Output $file
} else {
    Write-Error "No image in clipboard"
    exit 1
}`
	out, err := execCmd("powershell", "-NoProfile", "-Command", script)
	if err != nil {
		return nil, fmt.Errorf("%w", ErrNoImage)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return nil, fmt.Errorf("%w", ErrNoImage)
	}
	data, err := os.ReadFile(path)
	os.Remove(path)
	if err != nil {
		return nil, fmt.Errorf("reading temp clipboard file: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w", ErrNoImage)
	}
	return data, nil
}
