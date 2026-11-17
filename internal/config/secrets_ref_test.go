package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeychainRef_RoundTripsThroughParse(t *testing.T) {
	ref := KeychainRef("pmx-lab.fleet.demo", "pmx@pve!pmx")
	assert.Equal(t, "keychain:pmx-lab.fleet.demo/pmx@pve!pmx", ref)

	service, account, ok := ParseKeychainRef(ref)
	assert.True(t, ok)
	assert.Equal(t, "pmx-lab.fleet.demo", service)
	assert.Equal(t, "pmx@pve!pmx", account)
}

func TestParseKeychainRef(t *testing.T) {
	tests := []struct {
		ref, service, account string
		ok                    bool
	}{
		{"keychain:svc/acct", "svc", "acct", true},
		{"keychain:svc", "svc", "", true},
		{"keychain:svc/a/b", "svc", "a/b", true}, // the first "/" splits, as keychainLookup does
		{"keychain:", "", "", false},
		{"keychain:/acct", "", "", false},
		{"${VAR}", "", "", false},
		{"literal", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			service, account, ok := ParseKeychainRef(tt.ref)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.service, service)
			assert.Equal(t, tt.account, account)
		})
	}
}
