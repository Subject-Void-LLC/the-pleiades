// The HPKE suite identifiers the package documentation gives another
// implementation, held to the ones the code seals with.
package sealed

import (
	"crypto/hpke"
	"testing"
)

func TestSuite_IsTheOneThePackageDocumentationNames(t *testing.T) {
	if got := kem().ID(); got != 0x647a {
		t.Errorf("KEM id 0x%04x, documented as 0x647a", got)
	}
	if got := hpke.HKDFSHA256().ID(); got != 0x0001 {
		t.Errorf("KDF id 0x%04x, documented as 0x0001", got)
	}
	if got := hpke.AES256GCM().ID(); got != 0x0002 {
		t.Errorf("AEAD id 0x%04x, documented as 0x0002", got)
	}
}
