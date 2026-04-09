package dave

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"strconv"

	"github.com/cloudflare/circl/hpke"
	"golang.org/x/crypto/hkdf"
)

const (
	mlsVersion10             uint16 = 1
	mlsCipherSuiteID         uint16 = 2
	mlsLeafNodeSourceKeyPkg  uint8  = 1
	mlsCredentialTypeBasic   uint8  = 1
	ExportLabel                     = "Discord Secure Frames v0"
)

// ---------------------------------------------------------------------------
// TLS-style reader/writer for MLS binary encoding
// ---------------------------------------------------------------------------

type tlsWriter struct{ buf []byte }

func (w *tlsWriter) writeUint8(v uint8)   { w.buf = append(w.buf, v) }
func (w *tlsWriter) writeUint16(v uint16) { w.buf = append(w.buf, byte(v>>8), byte(v)) }
func (w *tlsWriter) writeUint32(v uint32) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	w.buf = append(w.buf, b...)
}
func (w *tlsWriter) writeUint64(v uint64) {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	w.buf = append(w.buf, b...)
}
func (w *tlsWriter) writeVec(data []byte) {
	w.writeVarint(uint64(len(data)))
	w.buf = append(w.buf, data...)
}
func (w *tlsWriter) writeVarint(v uint64) {
	switch {
	case v <= 63:
		w.buf = append(w.buf, byte(v))
	case v <= 16383:
		w.buf = append(w.buf, byte(0x40|v>>8), byte(v))
	case v <= 1073741823:
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(v)|0x80000000)
		w.buf = append(w.buf, b...)
	default:
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, v|0xC000000000000000)
		w.buf = append(w.buf, b...)
	}
}
func (w *tlsWriter) writeRaw(data []byte) { w.buf = append(w.buf, data...) }
func (w *tlsWriter) bytes() []byte        { return w.buf }

type tlsReader struct {
	data []byte
	pos  int
	err  error
}

func (r *tlsReader) remaining() int { return len(r.data) - r.pos }

func (r *tlsReader) readUint8() uint8 {
	if r.err != nil || r.pos+1 > len(r.data) {
		r.err = fmt.Errorf("short read uint8 at %d", r.pos)
		return 0
	}
	v := r.data[r.pos]
	r.pos++
	return v
}

func (r *tlsReader) readUint16() uint16 {
	if r.err != nil || r.pos+2 > len(r.data) {
		r.err = fmt.Errorf("short read uint16 at %d", r.pos)
		return 0
	}
	v := binary.BigEndian.Uint16(r.data[r.pos:])
	r.pos += 2
	return v
}

func (r *tlsReader) readUint64() uint64 {
	if r.err != nil || r.pos+8 > len(r.data) {
		r.err = fmt.Errorf("short read uint64 at %d", r.pos)
		return 0
	}
	v := binary.BigEndian.Uint64(r.data[r.pos:])
	r.pos += 8
	return v
}

func (r *tlsReader) readVec() []byte {
	if r.err != nil {
		return nil
	}
	length := r.readVarint()
	if r.err != nil {
		return nil
	}
	n := int(length)
	if r.pos+n > len(r.data) {
		r.err = fmt.Errorf("short read vec len=%d at %d", n, r.pos)
		return nil
	}
	out := make([]byte, n)
	copy(out, r.data[r.pos:r.pos+n])
	r.pos += n
	return out
}

func (r *tlsReader) readVarint() uint64 {
	if r.err != nil || r.pos >= len(r.data) {
		r.err = fmt.Errorf("short read varint at %d", r.pos)
		return 0
	}
	first := r.data[r.pos]
	kind := first >> 6
	switch kind {
	case 0:
		r.pos++
		return uint64(first & 0x3F)
	case 1:
		if r.pos+2 > len(r.data) {
			r.err = fmt.Errorf("short read varint(2) at %d", r.pos)
			return 0
		}
		v := uint64(first&0x3F)<<8 | uint64(r.data[r.pos+1])
		r.pos += 2
		return v
	case 2:
		if r.pos+4 > len(r.data) {
			r.err = fmt.Errorf("short read varint(4) at %d", r.pos)
			return 0
		}
		v := uint64(first&0x3F)<<24 | uint64(r.data[r.pos+1])<<16 | uint64(r.data[r.pos+2])<<8 | uint64(r.data[r.pos+3])
		r.pos += 4
		return v
	default:
		if r.pos+8 > len(r.data) {
			r.err = fmt.Errorf("short read varint(8) at %d", r.pos)
			return 0
		}
		v := uint64(first&0x3F)<<56 | uint64(r.data[r.pos+1])<<48 | uint64(r.data[r.pos+2])<<40 | uint64(r.data[r.pos+3])<<32 |
			uint64(r.data[r.pos+4])<<24 | uint64(r.data[r.pos+5])<<16 | uint64(r.data[r.pos+6])<<8 | uint64(r.data[r.pos+7])
		r.pos += 8
		return v
	}
}

// ---------------------------------------------------------------------------
// MLS key derivation
// ---------------------------------------------------------------------------

func MLSExpandWithLabel(secret []byte, label string, context []byte, length int) ([]byte, error) {
	mlsLabel := []byte("MLS 1.0 " + label)
	w := &tlsWriter{}
	w.writeUint16(uint16(length))
	w.writeVec(mlsLabel)
	w.writeVec(context)
	r := hkdf.Expand(sha256.New, secret, w.bytes())
	out := make([]byte, length)
	_, err := io.ReadFull(r, out)
	return out, err
}

func MLSDeriveSecret(secret []byte, label string) ([]byte, error) {
	return MLSExpandWithLabel(secret, label, []byte{}, 32)
}

func MLSExport(exporterSecret []byte, label string, context []byte, length int) ([]byte, error) {
	derivedSecret, err := MLSDeriveSecret(exporterSecret, label)
	if err != nil {
		return nil, err
	}
	contextHash := sha256.Sum256(context)
	return MLSExpandWithLabel(derivedSecret, "exported", contextHash[:], length)
}

func HKDFExtract(salt, ikm []byte) []byte {
	if salt == nil {
		salt = make([]byte, 32)
	}
	return hkdf.Extract(sha256.New, ikm, salt)
}

// ---------------------------------------------------------------------------
// HPKE + signatures
// ---------------------------------------------------------------------------

type hpkeKeyPair struct{ pub, priv []byte }

func generateHPKEKeyPair() (*hpkeKeyPair, error) {
	scheme := hpke.KEM_P256_HKDF_SHA256.Scheme()
	pub, priv, err := scheme.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	pubBytes, _ := pub.MarshalBinary()
	privBytes, _ := priv.MarshalBinary()
	return &hpkeKeyPair{pub: pubBytes, priv: privBytes}, nil
}

func hpkeDecrypt(privKeyBytes, kemOutput, info, aad, ciphertext []byte) ([]byte, error) {
	scheme := hpke.KEM_P256_HKDF_SHA256.Scheme()
	priv, err := scheme.UnmarshalBinaryPrivateKey(privKeyBytes)
	if err != nil {
		return nil, err
	}
	suite := hpke.NewSuite(hpke.KEM_P256_HKDF_SHA256, hpke.KDF_HKDF_SHA256, hpke.AEAD_AES128GCM)
	opener, err := suite.NewReceiver(priv, info)
	if err != nil {
		return nil, err
	}
	ctx, err := opener.Setup(kemOutput)
	if err != nil {
		return nil, err
	}
	return ctx.Open(ciphertext, aad)
}

type signatureKeyPair struct {
	pub  []byte
	priv *ecdsa.PrivateKey
}

func generateSignatureKeyPair() (*signatureKeyPair, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y)
	return &signatureKeyPair{pub: pub, priv: priv}, nil
}

func signWithLabel(key *signatureKeyPair, label string, content []byte) ([]byte, error) {
	w := &tlsWriter{}
	w.writeVec([]byte("MLS 1.0 " + label))
	w.writeVec(content)
	hash := sha256.Sum256(w.bytes())
	r, s, err := ecdsa.Sign(rand.Reader, key.priv, hash[:])
	if err != nil {
		return nil, err
	}
	return marshalECDSASig(r, s), nil
}

func marshalECDSASig(r, s *big.Int) []byte {
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	if len(rBytes) > 0 && rBytes[0]&0x80 != 0 {
		rBytes = append([]byte{0}, rBytes...)
	}
	if len(sBytes) > 0 && sBytes[0]&0x80 != 0 {
		sBytes = append([]byte{0}, sBytes...)
	}
	totalLen := 2 + len(rBytes) + 2 + len(sBytes)
	sig := make([]byte, 0, 2+totalLen)
	sig = append(sig, 0x30, byte(totalLen))
	sig = append(sig, 0x02, byte(len(rBytes)))
	sig = append(sig, rBytes...)
	sig = append(sig, 0x02, byte(len(sBytes)))
	sig = append(sig, sBytes...)
	return sig
}

func mlsRefHash(label string, value []byte) []byte {
	w := &tlsWriter{}
	w.writeVec([]byte(label))
	w.writeVec(value)
	h := sha256.Sum256(w.bytes())
	return h[:]
}

func keyPackageRef(serialized []byte) []byte {
	return mlsRefHash("MLS 1.0 KeyPackage Reference", serialized)
}

// ---------------------------------------------------------------------------
// Key package generation
// ---------------------------------------------------------------------------

// KeyPackageBundle holds a generated MLS key package and its private keys.
type KeyPackageBundle struct {
	Serialized     []byte
	InitPriv       []byte
	SigKey         *signatureKeyPair
	EncryptionPriv []byte
}

// GenerateKeyPackage generates an MLS key package for the given user ID.
func GenerateKeyPackage(userID string) (*KeyPackageBundle, error) {
	initKP, err := generateHPKEKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate init key: %w", err)
	}
	encKP, err := generateHPKEKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate enc key: %w", err)
	}
	sigKP, err := generateSignatureKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate sig key: %w", err)
	}

	userIDNum, err := strconv.ParseUint(userID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse user ID: %w", err)
	}
	identity := make([]byte, 8)
	binary.BigEndian.PutUint64(identity, userIDNum)

	leafContent := buildLeafNodeContent(encKP.pub, sigKP.pub, identity)
	leafSig, err := signWithLabel(sigKP, "LeafNodeTBS", leafContent)
	if err != nil {
		return nil, err
	}

	leafNode := &tlsWriter{}
	leafNode.writeRaw(leafContent)
	leafNode.writeVec(leafSig)

	kpContent := buildKeyPackageContent(initKP.pub, leafNode.bytes())
	kpSig, err := signWithLabel(sigKP, "KeyPackageTBS", kpContent)
	if err != nil {
		return nil, err
	}

	kpFull := &tlsWriter{}
	kpFull.writeRaw(kpContent)
	kpFull.writeVec(kpSig)

	return &KeyPackageBundle{
		Serialized:     kpFull.bytes(),
		InitPriv:       initKP.priv,
		SigKey:         sigKP,
		EncryptionPriv: encKP.priv,
	}, nil
}

func buildLeafNodeContent(encryptionKey, signatureKey, identity []byte) []byte {
	w := &tlsWriter{}
	w.writeVec(encryptionKey)
	w.writeVec(signatureKey)
	w.writeUint16(uint16(mlsCredentialTypeBasic))
	w.writeVec(identity)
	versionsData := make([]byte, 2)
	binary.BigEndian.PutUint16(versionsData, mlsVersion10)
	w.writeVec(versionsData)
	csData := make([]byte, 2)
	binary.BigEndian.PutUint16(csData, mlsCipherSuiteID)
	w.writeVec(csData)
	w.writeVec(nil)
	w.writeVec(nil)
	credData := make([]byte, 2)
	binary.BigEndian.PutUint16(credData, uint16(mlsCredentialTypeBasic))
	w.writeVec(credData)
	w.writeUint8(mlsLeafNodeSourceKeyPkg)
	w.writeUint64(0)
	w.writeUint64(^uint64(0))
	w.writeVec(nil)
	return w.bytes()
}

func buildKeyPackageContent(initKey, leafNode []byte) []byte {
	w := &tlsWriter{}
	w.writeUint16(mlsVersion10)
	w.writeUint16(mlsCipherSuiteID)
	w.writeVec(initKey)
	w.writeRaw(leafNode)
	w.writeVec(nil)
	return w.bytes()
}

// ---------------------------------------------------------------------------
// Welcome processing
// ---------------------------------------------------------------------------

// WelcomeResult holds the result of processing an MLS Welcome.
type WelcomeResult struct {
	ExporterSecret []byte
	Epoch          uint64
	GroupID        []byte
}

// ProcessWelcome processes an MLS Welcome message using the given key package.
func ProcessWelcome(data []byte, kpBundle *KeyPackageBundle) (*WelcomeResult, error) {
	r := &tlsReader{data: data}

	cipherSuite := r.readUint16()
	if r.err != nil {
		return nil, fmt.Errorf("read cipher suite: %w", r.err)
	}
	if cipherSuite != mlsCipherSuiteID {
		return nil, fmt.Errorf("unexpected cipher suite: %d", cipherSuite)
	}

	secretsData := r.readVec()
	if r.err != nil {
		return nil, fmt.Errorf("read secrets: %w", r.err)
	}
	encryptedGroupInfo := r.readVec()
	if r.err != nil {
		return nil, fmt.Errorf("read encrypted group info: %w", r.err)
	}

	ourRef := keyPackageRef(kpBundle.Serialized)

	sr := &tlsReader{data: secretsData}
	var kemOutput, encryptedSecrets []byte
	found := false
	for sr.remaining() > 0 && sr.err == nil {
		newMember := sr.readVec()
		kemOut := sr.readVec()
		ct := sr.readVec()
		if sr.err != nil {
			break
		}
		if bytesEqual(newMember, ourRef) {
			kemOutput = kemOut
			encryptedSecrets = ct
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("no matching EncryptedGroupSecrets for our KeyPackageRef")
	}

	infoW := &tlsWriter{}
	infoW.writeVec([]byte("MLS 1.0 Welcome"))
	infoW.writeVec(encryptedGroupInfo)

	groupSecretsPlain, err := hpkeDecrypt(kpBundle.InitPriv, kemOutput, infoW.bytes(), nil, encryptedSecrets)
	if err != nil {
		return nil, fmt.Errorf("HPKE decrypt: %w", err)
	}

	gsr := &tlsReader{data: groupSecretsPlain}
	joinerSecret := gsr.readVec()
	if gsr.err != nil {
		return nil, fmt.Errorf("read joiner secret: %w", gsr.err)
	}
	if gsr.readUint8() == 1 {
		_ = gsr.readVec() // path secret
	}

	pskSecret := make([]byte, 32)
	memberSecret := HKDFExtract(joinerSecret, pskSecret)

	welcomeSecret, err := MLSExpandWithLabel(memberSecret, "welcome", nil, 32)
	if err != nil {
		return nil, err
	}
	welcomeKey, err := MLSExpandWithLabel(welcomeSecret, "key", nil, 16)
	if err != nil {
		return nil, err
	}
	welcomeNonce, err := MLSExpandWithLabel(welcomeSecret, "nonce", nil, 12)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(welcomeKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	groupInfoPlain, err := gcm.Open(nil, welcomeNonce, encryptedGroupInfo, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt GroupInfo: %w", err)
	}

	groupContext, epoch, groupID, err := parseGroupInfoForContext(groupInfoPlain)
	if err != nil {
		return nil, err
	}

	epochSecret, err := MLSExpandWithLabel(memberSecret, "epoch", groupContext, 32)
	if err != nil {
		return nil, err
	}
	exporterSecret, err := MLSDeriveSecret(epochSecret, "exporter")
	if err != nil {
		return nil, err
	}

	return &WelcomeResult{
		ExporterSecret: exporterSecret,
		Epoch:          epoch,
		GroupID:        groupID,
	}, nil
}

func parseGroupInfoForContext(data []byte) (groupContext []byte, epoch uint64, groupID []byte, err error) {
	r := &tlsReader{data: data}
	ctxStart := r.pos
	_ = r.readUint16() // version
	_ = r.readUint16() // cipher suite
	groupID = r.readVec()
	epoch = r.readUint64()
	_ = r.readVec() // tree hash
	_ = r.readVec() // confirmed transcript hash
	_ = r.readVec() // extensions
	if r.err != nil {
		return nil, 0, nil, r.err
	}
	groupContext = make([]byte, r.pos-ctxStart)
	copy(groupContext, data[ctxStart:r.pos])
	return
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Hash ratchet key derivation (matches discordgo)
// ---------------------------------------------------------------------------

// HashRatchetGetKey derives the AES-128 key for a given generation.
func HashRatchetGetKey(baseSecret []byte, generation uint32) ([]byte, error) {
	secret := baseSecret
	for i := uint32(0); i < generation; i++ {
		genCtx := make([]byte, 4)
		binary.BigEndian.PutUint32(genCtx, i)
		next, err := MLSExpandWithLabel(secret, "secret", genCtx, 32)
		if err != nil {
			return nil, err
		}
		secret = next
	}
	genCtx := make([]byte, 4)
	binary.BigEndian.PutUint32(genCtx, generation)
	return MLSExpandWithLabel(secret, "key", genCtx, 16)
}
