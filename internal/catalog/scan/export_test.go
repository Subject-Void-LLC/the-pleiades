// What the scan_test package's tests reach inside this one.
package scan

import "testing"

// UseImages points the three scanners at other images for one test, and
// back at the pinned ones when it ends.
func UseImages(t testing.TB, osv, syft, gitleaks string) {
	was := [3]string{osvScannerImage, syftImage, gitleaksImage}
	osvScannerImage, syftImage, gitleaksImage = osv, syft, gitleaks
	t.Cleanup(func() { osvScannerImage, syftImage, gitleaksImage = was[0], was[1], was[2] })
}
