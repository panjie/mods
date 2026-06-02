//go:build darwin

package clipboard

import (
	"fmt"
	"os"
	"path/filepath"
)

func readImage() ([]byte, error) {
	return readImageOSAScript()
}

func readImageOSAScript() ([]byte, error) {
	tmpPath := filepath.Join(tempDir(), "mods-clipboard.png")
	script := fmt.Sprintf(`
use framework "AppKit"
set pb to current application's NSPasteboard's generalPasteboard
set imgData to pb's dataForType:(current application's NSPasteboardTypePNG)
if imgData is missing value then
	error "No image in clipboard"
end if
imgData's writeToFile:"%s" atomically:false
`, tmpPath)
	_, err := execCmd("osascript", "-e", script)
	if err != nil {
		return nil, fmt.Errorf("%w", ErrNoImage)
	}
	defer os.Remove(tmpPath)
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("reading temp clipboard file: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w", ErrNoImage)
	}
	return data, nil
}
