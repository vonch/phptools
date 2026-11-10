package packages

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

func TestDetachedSignaturePinsFullFingerprintAndPayload(t *testing.T) {
	entity, err := openpgp.NewEntity("PHPTools test", "", "test@example.invalid", &packet.Config{RSABits: 2048})
	if err != nil {
		t.Fatal(err)
	}
	var key bytes.Buffer
	encoded, err := armor.Encode(&key, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.Serialize(encoded); err != nil {
		t.Fatal(err)
	}
	encoded.Close()
	payload := []byte("a real signed archive payload")
	var signature bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&signature, entity, bytes.NewReader(payload), nil); err != nil {
		t.Fatal(err)
	}
	fingerprint := hex.EncodeToString(entity.PrimaryKey.Fingerprint)
	if err := verifyDetached(context.Background(), bytes.NewReader(payload), signature.Bytes(), key.Bytes(), fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := verifyDetached(context.Background(), bytes.NewReader([]byte("tampered")), signature.Bytes(), key.Bytes(), fingerprint); err == nil {
		t.Fatal("tampered payload verified")
	}
	if err := verifyDetached(context.Background(), bytes.NewReader(payload), signature.Bytes(), key.Bytes(), strings.Repeat("0", 40)); err == nil {
		t.Fatal("wrong fingerprint verified")
	}
}

// Opt-in because this reaches publisher servers and downloads release ZIPs.
// PHPTOOLS_NETWORK_TEST=refresh runs only discovery; nginx/mysql/redis/php run
// a genuine download, cryptographic verification and safe extraction in Temp.
func TestPublisherNetwork(t *testing.T) {
	mode := os.Getenv("PHPTOOLS_NETWORK_TEST")
	if mode == "" {
		t.Skip("set PHPTOOLS_NETWORK_TEST for live publisher verification")
	}
	ctx := context.Background()
	if mode == "refresh" {
		pkgs, diagnostics, err := Refresh(ctx, nil)
		for _, d := range diagnostics {
			t.Log(d.Message)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pkgs {
			t.Logf("%s %s %v", p.ID, p.Channel, p.Verified)
		}
		return
	}
	versions := map[string]string{"php": "5.6.40", "nginx": "1.30.6", "mysql": "5.7.44", "redis": "8.10.2"}
	for _, p := range SeedCatalog() {
		if p.Kind == mode && p.Version == versions[mode] {
			root := t.TempDir()
			installed, err := Install(ctx, root, p, nil)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := Install(ctx, root, p, nil)
			if err != nil || recovered.Path != installed.Path || recovered.InstalledAt != installed.InstalledAt || recovered.Arch != installed.Arch {
				t.Fatalf("recovery failed: %+v %v", recovered, err)
			}
			t.Logf("verified %s arch=%s; recovered existing install at %s", installed.ID, installed.Arch, installed.Path)
			return
		}
	}
	t.Fatalf("unknown network test mode %s", mode)
}
