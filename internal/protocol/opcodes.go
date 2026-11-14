package protocol

// Values are the Legacy branch's src/properties/{send,recv}.properties.
const (
	RecvHello           uint16 = 0x14
	RecvLogin           uint16 = 0x15
	RecvCharList        uint16 = 0x19
	RecvSelect          uint16 = 0x1a
	RecvLoggedIn        uint16 = 0x1b
	RecvCheckName       uint16 = 0x1c
	RecvCreate          uint16 = 0x1d
	RecvDelete          uint16 = 0x20
	RecvClientStart     uint16 = 0x21
	RecvClientError     uint16 = 0x22
	RecvGetServer       uint16 = 0x29 // v148 CLogin::Init (0x5B0DBC) sends it bodyless; v145 sent 0x27.
	RecvPong            uint16 = 0x2c
	RecvSetGender       uint16 = 0x3d
	RecvLogout          uint16 = 0x3e
	RecvRedisplay       uint16 = 0x40
	RecvChangeMap       uint16 = 0x47
	RecvChangeChannel   uint16 = 0x48
	RecvEnterCashshop   uint16 = 0x49
	RecvMove            uint16 = 0x51
	SendLoginStatus     uint16 = 0
	SendWorldList       uint16 = 2
	SendCharacterList   uint16 = 0x3
	SendServerIP        uint16 = 0x4
	SendNameResult      uint16 = 5
	SendCreateResult    uint16 = 6
	SendChannelChange   uint16 = 8
	SendShowMapleStory  uint16 = 9
	SendRecommended     uint16 = 0x11
	SendChooseGender    uint16 = 0x21
	SendGenderSet       uint16 = 0x20
	SendServerStatus    uint16 = 0x25
	SendLoginBackground uint16 = 0x26
	SendUpdateStats     uint16 = 0x2c
	SendSetField        uint16 = 0xd1
	SendServerBlocked   uint16 = 0xd9
	SendKeymap          uint16 = 0x275
)
