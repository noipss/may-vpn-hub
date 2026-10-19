package tunnel

import "testing"

func TestParseNetstat(t *testing.T) {
	out := `Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
utun7      1380  <Link#15>                            9     0       1234        2     0        200     0
utun7      1380  fe80::2a6b: fe80:f::2a6b:d8eb        0     -          0        2     -        200     -
`
	rx, tx := parseNetstat(out, "utun7")
	if rx != 1234 || tx != 200 {
		t.Fatalf("rx=%d tx=%d", rx, tx)
	}
}
