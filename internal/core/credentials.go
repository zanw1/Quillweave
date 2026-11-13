package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"math"
	"os"
	"path/filepath"
	websearch "quillweave/internal/search"
	"runtime"
	"strings"
)

const credentialSchema = `CREATE TABLE IF NOT EXISTS credentials(
 scope TEXT NOT NULL CHECK(scope IN ('model','search')),
 profile_id TEXT NOT NULL,
 identity TEXT NOT NULL,
 encrypted BLOB NOT NULL,
 generation INTEGER NOT NULL CHECK(generation>0),
 PRIMARY KEY(scope,profile_id));`

// credentialCipher opens the independent installation key and never replaces a missing key for existing ciphertext.
func (store *Store) credentialCipher(db documentDB, create bool) (cipher.AEAD, error) {
	path := store.path + ".key"
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && create {
		var count int
		if err := db.QueryRowContext(background(), "SELECT COUNT(*) FROM credentials WHERE length(encrypted)>0").Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, errors.New("The credential master key file is missing. Restore it to unlock saved API keys")
		}
		key, err = createCredentialKey(path)
		if err != nil {
			return nil, errors.New("Cannot create the credential master key file. Check the data directory permissions")
		}
	}
	if err != nil {
		return nil, errors.New("Cannot read the credential master key file. Restore it to unlock saved API keys")
	}
	if len(key) != 32 {
		return nil, errors.New("The credential master key file is invalid. Restore the original file")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0600); err != nil {
			return nil, errors.New("Cannot restrict access to the credential master key file")
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

// createCredentialKey publishes a complete random key atomically without overwriting another writer's key.
func createCredentialKey(path string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".credential-key-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(key); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return nil, err
	}
	if err := os.Link(file.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ReadFile(path)
		}
		return nil, err
	}
	if runtime.GOOS != "windows" {
		directory, err := os.Open(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		err = errors.Join(directory.Sync(), directory.Close())
		if err != nil {
			return nil, err
		}
	}
	return key, nil
}

// readCredentials authenticates each ciphertext against its scope, profile and destination before use.
func (store *Store) readCredentials(db documentDB, scope string) (map[string]searchSecret, error) {
	rows, err := db.QueryContext(background(), "SELECT profile_id,identity,encrypted,generation FROM credentials WHERE scope=?", scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]searchSecret{}
	var aead cipher.AEAD
	for rows.Next() {
		var id string
		var encrypted []byte
		var secret searchSecret
		if err := rows.Scan(&id, &secret.Identity, &encrypted, &secret.Generation); err != nil {
			return nil, err
		}
		if len(encrypted) > 0 {
			if aead == nil {
				aead, err = store.credentialCipher(db, false)
				if err != nil {
					return nil, err
				}
			}
			if encrypted[0] != 1 {
				return nil, errors.New("Unsupported encrypted credential version")
			}
			plain, err := aead.Open(nil, nil, encrypted[1:], []byte(jsonText([]string{scope, id, secret.Identity})))
			if err != nil {
				return nil, errors.New("Saved API keys could not be decrypted. Restore the matching master key file and database")
			}
			secret.Value = string(plain)
		}
		result[id] = secret
	}
	return result, rows.Err()
}

// saveCredentials commits replacements, explicit clears and removed profiles inside the caller's configuration transaction.
func (store *Store) saveCredentials(db documentDB, scope string, desired map[string]searchSecret) error {
	previous, err := store.readCredentials(db, scope)
	if err != nil {
		return err
	}
	var aead cipher.AEAD
	for id, secret := range desired {
		old, exists := previous[id]
		if exists && old.Value == secret.Value && old.Identity == secret.Identity && secret.Generation <= old.Generation {
			continue
		}
		if old.Generation >= math.MaxInt64 {
			return errors.New("Credential revision is out of range")
		}
		encrypted := []byte{}
		if secret.Value != "" {
			if aead == nil {
				aead, err = store.credentialCipher(db, true)
				if err != nil {
					return err
				}
			}
			encrypted = aead.Seal([]byte{1}, nil, []byte(secret.Value), []byte(jsonText([]string{scope, id, secret.Identity})))
		}
		if _, err := db.ExecContext(background(), "INSERT INTO credentials(scope,profile_id,identity,encrypted,generation) VALUES(?,?,?,?,?) ON CONFLICT(scope,profile_id) DO UPDATE SET identity=excluded.identity,encrypted=excluded.encrypted,generation=excluded.generation", scope, id, secret.Identity, encrypted, old.Generation+1); err != nil {
			return err
		}
	}
	for id := range previous {
		if _, exists := desired[id]; !exists {
			if _, err := db.ExecContext(background(), "DELETE FROM credentials WHERE scope=? AND profile_id=?", scope, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// modelCredentialIdentity binds a key to one protocol and normalized service endpoint.
func modelCredentialIdentity(profile ModelProfile) string {
	return jsonText([]string{profile.Config.Protocol, strings.TrimRight(profile.Config.BaseURL, "/")})
}

// restoreModelCredentials restores matching saved keys and uses the environment only for an unconfigured default profile.
func (store *Store) restoreModelCredentials(db documentDB, models *ModelSettings) error {
	keys, err := store.readCredentials(db, "model")
	if err != nil {
		return err
	}
	for i := range models.Profiles {
		profile := &models.Profiles[i]
		profile.Config.APIKey = ""
		secret, exists := keys[profile.ID]
		if exists && secret.Identity == modelCredentialIdentity(*profile) {
			profile.Config.APIKey = secret.Value
		} else if !exists && profile.ID == models.DefaultProfile {
			profile.Config.APIKey = os.Getenv("QUILLWEAVE_API_KEY")
		}
	}
	return nil
}

// runtimeModels reads configuration and encrypted credentials from one consistent database snapshot.
func (store *Store) runtimeModels() (ModelSettings, error) {
	var models ModelSettings
	err := store.withReadSnapshot(func(db documentDB) error {
		var err error
		models, err = readModelSettings(db)
		if err != nil {
			return err
		}
		return store.restoreModelCredentials(db, &models)
	})
	return models, err
}

// saveModelCredentials persists only each model's destination-bound secret, separately from public configuration.
func (store *Store) saveModelCredentials(db documentDB, models ModelSettings) error {
	keys := map[string]searchSecret{}
	for _, profile := range models.Profiles {
		keys[profile.ID] = searchSecret{Value: profile.Config.APIKey, Identity: modelCredentialIdentity(profile)}
	}
	return store.saveCredentials(db, "model", keys)
}

// saveSearchCredentials retains local keys only for the exact service identity and removes deleted or referenced secrets.
func (store *Store) saveSearchCredentials(db documentDB, settings websearch.Settings, keys map[string]searchSecret) error {
	desired := map[string]searchSecret{}
	for _, profile := range settings.Profiles {
		if profile.CredentialSource == "model_ref" {
			continue
		}
		secret := keys[profile.ID]
		if secret.Identity != profile.Identity() {
			secret = searchSecret{Identity: profile.Identity()}
		}
		desired[profile.ID] = secret
	}
	return store.saveCredentials(db, "search", desired)
}

// refreshSearchCredentialsLocked refreshes persisted local secrets while keeping process-owned model-reference generations.
func (engine *Engine) refreshSearchCredentialsLocked(db documentDB) error {
	keys, err := engine.store.readCredentials(db, "search")
	if err != nil {
		return err
	}
	for id, secret := range engine.searchKeys {
		if strings.HasPrefix(id, "model-ref:") {
			keys[id] = secret
		}
	}
	engine.searchKeys = keys
	return nil
}
