package rpsr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_aclKey_Unique(t *testing.T) {
	acl1 := ACL{Principal: "fo", Resource: "obar"}
	acl2 := ACL{Principal: "foo", ResourceType: "bar"}
	require.NotEqual(t, aclKey(acl1), aclKey(acl2))
}
