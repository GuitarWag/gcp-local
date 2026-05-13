package state

import (
	"strings"

	bolt "go.etcd.io/bbolt"
)

type Bolt struct {
	db *bolt.DB
}

func NewBolt(path string) (*Bolt, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	return &Bolt{db: db}, nil
}

func (b *Bolt) Get(namespace, key string) ([]byte, error) {
	var out []byte
	err := b.db.View(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte(namespace))
		if bk == nil {
			return ErrNotFound
		}
		v := bk.Get([]byte(key))
		if v == nil {
			return ErrNotFound
		}
		out = make([]byte, len(v))
		copy(out, v)
		return nil
	})
	return out, err
}

func (b *Bolt) Put(namespace, key string, value []byte) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bk, err := tx.CreateBucketIfNotExists([]byte(namespace))
		if err != nil {
			return err
		}
		return bk.Put([]byte(key), value)
	})
}

func (b *Bolt) Delete(namespace, key string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte(namespace))
		if bk == nil {
			return ErrNotFound
		}
		if bk.Get([]byte(key)) == nil {
			return ErrNotFound
		}
		return bk.Delete([]byte(key))
	})
}

func (b *Bolt) List(namespace, prefix string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := b.db.View(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte(namespace))
		if bk == nil {
			return nil
		}
		return bk.ForEach(func(k, v []byte) error {
			ks := string(k)
			if prefix == "" || strings.HasPrefix(ks, prefix) {
				cp := make([]byte, len(v))
				copy(cp, v)
				out[ks] = cp
			}
			return nil
		})
	})
	return out, err
}

func (b *Bolt) Close() error { return b.db.Close() }
