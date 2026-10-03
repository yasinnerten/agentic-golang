package loopcontroller

import "testing"

func TestQuoteIdentifier(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tenant_acme", `"tenant_acme"`},
		{"tenant_Mixed-Case.1", `"tenant_Mixed-Case.1"`},
		{`tenant_x"; DROP TABLE users; --`, `"tenant_x""; DROP TABLE users; --"`},
		{`a""b`, `"a""""b"`},
		{"tenant_a\x00\"; DROP", `"tenant_a"`},
	}
	for _, c := range cases {
		if got := quoteIdentifier(c.in); got != c.want {
			t.Errorf("quoteIdentifier(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestTenantSchemaIsQuoted(t *testing.T) {
	got := quoteIdentifier(dbTenantSchema(`evil"`))
	if got != `"tenant_evil"""` {
		t.Fatalf("got %s", got)
	}
}
