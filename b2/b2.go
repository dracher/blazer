// Copyright 2016, Google
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package b2 provides a high-level interface to Backblaze's B2 cloud storage
// service.
package b2

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io"

	"golang.org/x/net/context"
)

// Client is a Backblaze B2 client.
type Client struct {
	backend beRootInterface
}

// NewClient creates and returns a new Client with valid B2 service account
// tokens.
func NewClient(ctx context.Context, account, key string) (*Client, error) {
	c := &Client{
		backend: &beRoot{
			b2i: &b2Root{},
		},
	}
	if err := c.backend.authorizeAccount(ctx, account, key); err != nil {
		return nil, err
	}
	return c, nil
}

// Bucket is a reference to a B2 bucket.
type Bucket struct {
	b beBucketInterface
	r beRootInterface
}

// Bucket returns the named bucket.  If the bucket already exists (and belongs
// to this account), it is reused.  Otherwise a new bucket is created.
func (c *Client) Bucket(ctx context.Context, name string) (*Bucket, error) {
	buckets, err := c.backend.listBuckets(ctx)
	if err != nil {
		return nil, err
	}
	for _, bucket := range buckets {
		if bucket.name() == name {
			return &Bucket{
				b: bucket,
				r: c.backend,
			}, nil
		}
	}
	b, err := c.backend.createBucket(ctx, name, "")
	if err != nil {
		return nil, err
	}
	return &Bucket{
		b: b,
		r: c.backend,
	}, err
}

// Delete removes a bucket.  The bucket must be empty.
func (b *Bucket) Delete(ctx context.Context) error {
	return b.b.deleteBucket(ctx)
}

// Object represents a B2 object.
type Object struct {
	name string
	f    beFileInterface
	b    *Bucket
}

// Object returns a reference to the named object in the bucket.  Hidden
// objects cannot be referenced in this manner; they can only be found by
// finding the appropriate reference in ListObjects.
func (b *Bucket) Object(name string) *Object {
	return &Object{
		name: name,
		b:    b,
	}
}

// NewWriter returns a new writer for the given object.  Objects that are
// overwritten are not deleted, but are "hidden".
func (o *Object) NewWriter(ctx context.Context) *Writer {
	bw := &Writer{
		o:    o,
		name: o.name,
		Info: make(map[string]string),
		chsh: sha1.New(),
		cbuf: &bytes.Buffer{},
		ctx:  ctx,
	}
	bw.w = io.MultiWriter(bw.chsh, bw.cbuf)
	return bw
}

// NewReader returns a reader for the given object.
func (o *Object) NewReader(ctx context.Context) *Reader {
	ctx, cancel := context.WithCancel(ctx)
	return &Reader{
		ctx:    ctx,
		cancel: cancel,
		o:      o,
		name:   o.name,
		chunks: make(map[int]*bytes.Buffer),
	}
}

func (o *Object) ensure(ctx context.Context) error {
	if o.f == nil {
		f, err := o.b.getObject(ctx, o.name)
		if err != nil {
			return err
		}
		o.f = f.f
	}
	return nil
}

// Delete removes the given object.
func (o *Object) Delete(ctx context.Context) error {
	if err := o.ensure(ctx); err != nil {
		return err
	}
	return o.f.deleteFileVersion(ctx)
}

// Cursor is passed to ListObjects to return subsequent pages.
type Cursor struct {
	name string
	id   string
}

// ListObjects returns objects in the bucket.  Cursor may be nil; when passed
// to a subsequent query, it will continue the listing.
func (b *Bucket) ListObjects(ctx context.Context, count int, c *Cursor) ([]*Object, *Cursor, error) {
	if c == nil {
		c = &Cursor{}
	}
	fs, name, id, err := b.b.listFileVersions(ctx, count, c.name, c.id)
	if err != nil {
		return nil, nil, err
	}
	next := &Cursor{
		name: name,
		id:   id,
	}
	var objects []*Object
	for _, f := range fs {
		objects = append(objects, &Object{
			name: f.name(),
			f:    f,
			b:    b,
		})
	}
	return objects, next, nil
}

func (b *Bucket) getObject(ctx context.Context, name string) (*Object, error) {
	fs, _, err := b.b.listFileNames(ctx, 1, name)
	if err != nil {
		return nil, err
	}
	if len(fs) < 1 {
		return nil, fmt.Errorf("%s: not found", name)
	}
	f := fs[0]
	if f.name() != name {
		return nil, fmt.Errorf("%s: not found", name)
	}
	return &Object{
		name: name,
		f:    f,
		b:    b,
	}, nil
}
