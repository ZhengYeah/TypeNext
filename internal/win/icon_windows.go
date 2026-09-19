//go:build windows && amd64

package win

import "fmt"

const appIconResource = 1 // assets/typenext.rc

var (
	pLoadImage   = user32.NewProc("LoadImageW")
	pDestroyIcon = user32.NewProc("DestroyIcon")
)

func loadAppIcon(instance uintptr, small bool) (uintptr, error) {
	widthMetric, heightMetric := uintptr(11), uintptr(12) // SM_CXICON, SM_CYICON
	if small {
		widthMetric, heightMetric = 49, 50 // SM_CXSMICON, SM_CYSMICON
	}
	width, _, _ := pGetSystemMetrics.Call(widthMetric)
	height, _, _ := pGetSystemMetrics.Call(heightMetric)
	// Load the embedded size nearest to the current display's icon metrics.
	// Without LR_SHARED, each handle is owned by the app and freed on exit.
	icon, _, err := pLoadImage.Call(instance, appIconResource, 1, width, height, 0) // IMAGE_ICON
	if icon == 0 {
		return 0, fmt.Errorf("load application icon: %v", err)
	}
	return icon, nil
}
