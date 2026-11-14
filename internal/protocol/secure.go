package protocol

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/chacha20"
)

// The launcher proves it is talking to this server before it applies anything
// the server says. After the usual hello the launcher sends an upgrade request
// with a one-time X25519 key; the server answers with its own one-time key and
// an Ed25519 signature over both keys and the hello. Everything the server
// sends after that is encrypted with a key only the two ends can derive, and
// every launcher packet (0x600-0x6FF) carries a tag the launcher checks.
// A client that does not ask keeps the old stream unless the key is required.
// Wire format: Open-Maple docs/SECURE_CHANNEL.md.
const (
	// LauncherKeyEnv holds the base64 Ed25519 seed (32 bytes) of this server.
	LauncherKeyEnv = "MAPLEGO_LAUNCHER_KEY"
	// LauncherKeyFileEnv names a file with the same content.
	LauncherKeyFileEnv = "MAPLEGO_LAUNCHER_KEY_FILE"
	// LauncherKeyRequiredEnv set to 1 refuses clients that do not upgrade.
	LauncherKeyRequiredEnv = "MAPLEGO_LAUNCHER_KEY_REQUIRED"

	secureMagic      = "OMKX\x01\x00\x00\x00"
	secureSignLabel  = "OpenMaple key exchange v1\x00"
	secureKeyLabel   = "OpenMaple session v1"
	secureTagSize    = 16
	secureRequestLen = len(secureMagic) + 32
)

// ErrLauncherKeyRequired reports a client that did not ask for the upgrade.
var ErrLauncherKeyRequired = errors.New("client did not start the launcher key exchange")

type launcherKey struct {
	private  ed25519.PrivateKey
	required bool
}

var (
	launcherKeyOnce  sync.Once
	launcherKeyValue *launcherKey
	launcherKeyErr   error
)

// LoadLauncherKey reads the signing key from the environment. It returns
// whether a key is configured; a malformed key is an error, never "no key".
func LoadLauncherKey() (configured bool, err error) {
	launcherKeyOnce.Do(func() {
		text := os.Getenv(LauncherKeyEnv)
		if path := os.Getenv(LauncherKeyFileEnv); text == "" && path != "" {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				launcherKeyErr = fmt.Errorf("%s: %w", LauncherKeyFileEnv, readErr)
				return
			}
			text = string(data)
		}
		required := os.Getenv(LauncherKeyRequiredEnv) == "1"
		text = strings.TrimSpace(text)
		if text == "" {
			if required {
				launcherKeyErr = fmt.Errorf("%s is set but no key is configured", LauncherKeyRequiredEnv)
			}
			return
		}
		private, parseErr := ParseLauncherKey(text)
		if parseErr != nil {
			launcherKeyErr = parseErr
			return
		}
		launcherKeyValue = &launcherKey{private: private, required: required}
	})
	return launcherKeyValue != nil, launcherKeyErr
}

// ParseLauncherKey decodes the base64 seed written by cmd/launcherkey.
func ParseLauncherKey(text string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("launcher key must be 32 bytes in base64")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func currentLauncherKey() (*launcherKey, error) {
	_, err := LoadLauncherKey()
	return launcherKeyValue, err
}

// secureSession is the state of one upgraded connection.
type secureSession struct {
	stream  *chacha20.Cipher
	macKey  [32]byte
	counter uint64
}

// deriveSecureSession turns the shared secret into the downlink cipher and
// the tag key. The salt and info bind the keys to this one exchange.
func deriveSecureSession(shared, hello, clientKey, serverKey []byte) (*secureSession, error) {
	salt := append(append([]byte(nil), clientKey...), serverKey...)
	info := append([]byte(secureKeyLabel), hello...)
	okm, err := hkdf.Key(sha256.New, shared, salt, string(info), 32+chacha20.NonceSize+32)
	if err != nil {
		return nil, err
	}
	stream, err := chacha20.NewUnauthenticatedCipher(okm[:32], okm[32:32+chacha20.NonceSize])
	if err != nil {
		return nil, err
	}
	s := &secureSession{stream: stream}
	copy(s.macKey[:], okm[32+chacha20.NonceSize:])
	return s, nil
}

func secureTranscript(hello, clientKey, serverKey []byte) []byte {
	t := make([]byte, 0, len(secureSignLabel)+len(hello)+64)
	t = append(t, secureSignLabel...)
	t = append(t, hello...)
	t = append(t, clientKey...)
	return append(t, serverKey...)
}

// launcherPacket reports whether the launcher, not the game, reads opcode.
func launcherPacket(packet []byte) bool {
	return len(packet) >= 2 && packet[1] == 0x06
}

// tag authenticates one launcher packet at its position in the connection.
func (s *secureSession) tag(packet []byte) [secureTagSize]byte {
	mac := hmac.New(sha256.New, s.macKey[:])
	var counter [8]byte
	binary.LittleEndian.PutUint64(counter[:], s.counter)
	mac.Write(counter[:])
	mac.Write(packet)
	s.counter++
	var out [secureTagSize]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// answerSecureRequest runs the server half of the exchange. hello is the
// plain hello already sent, request the client's upgrade request.
func answerSecureRequest(key ed25519.PrivateKey, hello, request []byte) (response []byte, session *secureSession, err error) {
	if len(request) != secureRequestLen || string(request[:len(secureMagic)]) != secureMagic {
		return nil, nil, fmt.Errorf("%w: launcher key request", ErrMalformed)
	}
	clientKey := request[len(secureMagic):]
	peer, err := ecdh.X25519().NewPublicKey(clientKey)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: launcher key", ErrMalformed)
	}
	own, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	// ECDH refuses the all-zero result of a low-order client key.
	shared, err := own.ECDH(peer)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: launcher key", ErrMalformed)
	}
	serverKey := own.PublicKey().Bytes()
	session, err = deriveSecureSession(shared, hello, clientKey, serverKey)
	if err != nil {
		return nil, nil, err
	}
	response = append(serverKey, ed25519.Sign(key, secureTranscript(hello, clientKey, serverKey))...)
	return response, session, nil
}

// requestSecureSession is the client half, used by Dial in tests and tools.
type secureRequest struct {
	private *ecdh.PrivateKey
	hello   []byte
}

func newSecureRequest(hello []byte) (*secureRequest, []byte, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	request := append([]byte(secureMagic), private.PublicKey().Bytes()...)
	return &secureRequest{private: private, hello: append([]byte(nil), hello...)}, request, nil
}

func (r *secureRequest) finish(server ed25519.PublicKey, response []byte) (*secureSession, error) {
	if len(response) != 32+ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: launcher key response", ErrMalformed)
	}
	serverKey, signature := response[:32], response[32:]
	clientKey := r.private.PublicKey().Bytes()
	if !ed25519.Verify(server, secureTranscript(r.hello, clientKey, serverKey), signature) {
		return nil, errors.New("launcher key response is not signed by the expected server")
	}
	peer, err := ecdh.X25519().NewPublicKey(serverKey)
	if err != nil {
		return nil, err
	}
	shared, err := r.private.ECDH(peer)
	if err != nil {
		return nil, err
	}
	return deriveSecureSession(shared, r.hello, clientKey, serverKey)
}
