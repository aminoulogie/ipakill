// Package fonts embeds Inter (rsms.me/inter, SIL Open Font License 1.1, see
// LICENSE-Inter.txt): the system font, standing in for Apple's San Francisco, which may not
// be used outside Apple's platforms. Inter was drawn for screens in the same spirit and is
// what most SF-style designs use when SF can't ship.
package fonts

import _ "embed"

var (
	//go:embed Inter-Regular.ttf
	InterRegular []byte
	//go:embed Inter-Medium.ttf
	InterMedium []byte
	//go:embed Inter-SemiBold.ttf
	InterSemiBold []byte
	//go:embed Inter-Bold.ttf
	InterBold []byte
)
