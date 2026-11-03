package doctor

import (
	"strings"
	"testing"
)

// build_chain.sh 3.18.0 refuses to generate a chain without `openssl dgst -keccak-256`; Ubuntu
// 24.04's OpenSSL 3.0 cannot do it, and the first 3.18.0 gate run died on exactly that.
func TestAnOpenSSLWithoutKeccakIsRefusedAsInfra(t *testing.T) {
	plan := []Requirement{{Name: "openssl-keccak", Kind: KindOpenSSLKeccak, ConfigKey: "tools.openssl_cmd", Why: "w"}}
	p := everything()
	p.keccak["openssl"] = false

	rep, err := CheckIn("/repo", plan, map[string]string{}, p)
	if err == nil || rep.Deps[0].Class != "infra" || !strings.Contains(rep.Deps[0].Detail, "keccak-256") {
		t.Errorf("an OpenSSL without keccak was accepted: %+v (%v)", rep.Deps[0], err)
	}
	// tools.openssl_cmd points the probe (and build_chain.sh, via OPENSSL_CMD) at another binary.
	p.keccak["/opt/tongsuo/bin/openssl"] = true
	if _, err := CheckIn("/repo", plan, map[string]string{"tools.openssl_cmd": "/opt/tongsuo/bin/openssl"}, p); err != nil {
		t.Errorf("a configured Tongsuo was refused: %v", err)
	}
}
