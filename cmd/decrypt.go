package cmd

import (
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ProtonMail/gosop/utils"

	"github.com/ProtonMail/gopenpgp/v3/constants"
	"github.com/ProtonMail/gopenpgp/v3/crypto"

	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

var symKeyAlgos = map[packet.CipherFunction]string{
	packet.Cipher3DES:   constants.ThreeDES,
	packet.CipherCAST5:  constants.CAST5,
	packet.CipherAES128: constants.AES128,
	packet.CipherAES192: constants.AES192,
	packet.CipherAES256: constants.AES256,
}

// Decrypt takes the data from stdin and decrypts it with the key file passed as
// argument, or a passphrase in a file passed with the --with-password flag.
// Note: Can't encrypt both symmetrically (passphrase) and keys.
// TODO: Multiple signers?
//
// --session-key-out=file flag: Outputs session key byte stream to given file.
func Decrypt(keyFilenames ...string) error {
	if len(keyFilenames) == 0 && password == "" && sessionKey == "" {
		println("Please provide decryption keys, session key, or passphrase")
		return Err69
	}
	var err error

	pgp := crypto.PGP()
	builder := pgp.Decryption()

	var pubKeyRing *crypto.KeyRing
	if verifyWith.Value() != nil {
		verifyKeys := utils.CollectFilesFromCliSlice(verifyWith.Value())
		pubKeyRing, err = utils.CollectKeys(verifyKeys...)
		if err != nil {
			return decErr(err)
		}
		builder.VerificationKeys(pubKeyRing)
	}
	if (verificationsOut == "" && pubKeyRing.CountEntities() != 0) ||
		(verificationsOut != "" && pubKeyRing.CountEntities() == 0) {
		return Err23
	}

	timeFrom, timeTo, err := utils.ParseDates(notBefore, notAfter)
	if err != nil {
		return decErr(err)
	}

	var sk *crypto.SessionKey
	if sessionKey != "" {
		sk, err = parseSessionKey()
		if err != nil {
			return decErr(err)
		}
		builder.SessionKey(sk)
	} else if password != "" {
		pw, err := utils.ReadFileOrEnv(password)
		if err != nil {
			return decErr(err)
		}
		pw = []byte(strings.TrimSpace(string(pw)))
		builder.Password(pw)
	} else {
		var pw []byte
		if keyPassword != "" {
			pw, err = utils.ReadSanitizedPassword(keyPassword)
			if err != nil {
				return decErr(err)
			}
		}
		privKeyRing, failUnlock, err := utils.CollectKeysPassword(pw, keyFilenames...)
		if failUnlock {
			return Err67
		}
		if err != nil {
			return decErr(err)
		}
		defer privKeyRing.ClearPrivateParams()
		builder.DecryptionKeys(privKeyRing)
	}

	if sessionKeyOut != "" {
		builder.RetrieveSessionKey()
	}

	decryptor, _ := builder.New()
	ptReader, err := decryptor.DecryptingReader(os.Stdin, crypto.Auto)
	if err != nil {
		return decErr(err)
	}
	_, err = io.Copy(os.Stdout, ptReader)
	if err != nil {
		return decErr(err)
	}

	if sessionKeyOut != "" {
		err = writeSessionKeyToFile(ptReader.SessionKey())
		if err != nil {
			return decErr(err)
		}
	}

	if verificationsOut != "" {
		result, err := ptReader.VerifySignature()
		if err != nil {
			return decErr(err)
		}
		result.ConstrainToTimeRange(timeFrom.Unix(), timeTo.Unix())
		if err := writeVerificationToFileFromResult(result); err != nil {
			return decErr(err)
		}
	}

	return nil
}

func decErr(err error) error {
	return Err99("decrypt", err)
}

func parseSessionKey() (*crypto.SessionKey, error) {
	formattedSessionKey, err := utils.ReadFileOrEnv(sessionKey)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimSpace(string(formattedSessionKey)), ":")
	skAlgo, err := strconv.ParseUint(parts[0], 10, 8)
	if err != nil {
		return nil, err
	}
	skAlgoName, ok := symKeyAlgos[packet.CipherFunction(skAlgo)]
	if !ok {
		return nil, errors.New("unsupported session key algorithm")
	}
	skBytes, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	sk := crypto.NewSessionKeyFromToken(skBytes, skAlgoName)
	return sk, nil
}

func writeSessionKeyToFile(sk *crypto.SessionKey) error {
	sessionKeyFile, err := utils.OpenOutFile(sessionKeyOut)
	if err != nil {
		return err
	}
	defer sessionKeyFile.Close()
	cipherFunc, err := sk.GetCipherFunc()
	if err != nil {
		return decErr(err)
	}
	formattedSessionKey := strconv.FormatUint(uint64(cipherFunc), 10) + ":" +
		strings.ToUpper(hex.EncodeToString(sk.Key))
	if _, err = sessionKeyFile.Write([]byte(formattedSessionKey)); err != nil {
		return decErr(err)
	}
	if err = sessionKeyFile.Close(); err != nil {
		return decErr(err)
	}
	return nil
}
