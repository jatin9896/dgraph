/*
 * Copyright 2017-2018 Dgraph Labs, Inc.
 *
 * This file is available under the Apache License, Version 2.0,
 * with the Commons Clause restriction.
 */

package cm

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
)

const (
	defaultDir      = "tls"
	defaultDays     = 1826
	defaultCACert   = "ca.crt"
	defaultCAKey    = "ca.key"
	defaultKeySize  = 2048
	defaultNodeCert = "node.crt"
	defaultNodeKey  = "node.key"
	keySizeTooSmall = 512
	keySizeTooLarge = 4096
)

const (
	forceCA = 1 << iota
	forceClient
	forceNode
)

// makeKey generates an RSA private key of bitSize length, storing it in the
// file fn. If force is true, the file is replaced.
// Returns the RSA private key, or error otherwise.
func makeKey(fn string, bitSize int, force bool) (*rsa.PrivateKey, error) {
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !force {
		flag |= os.O_EXCL
	}

	f, err := os.OpenFile(fn, flag, 0600)
	if err != nil {
		// reuse the existing key, if possible.
		if os.IsExist(err) {
			fmt.Printf("Using existing key file: %s\n", fn)
			return readKey(fn)
		}
		return nil, err
	}
	defer f.Close()

	key, err := rsa.GenerateKey(rand.Reader, bitSize)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Creating new key file: %s\n", fn)

	err = pem.Encode(f, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err != nil {
		return nil, err
	}

	return key, nil
}

// readKey tries to read and decode the contents of a private key at fn.
// Returns the RSA private key, or error otherwise.
func readKey(fn string) (*rsa.PrivateKey, error) {
	b, err := ioutil.ReadFile(fn)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(b)
	switch {
	case block == nil:
		return nil, fmt.Errorf("failed to read key block")
	case block.Type != "RSA PRIVATE KEY":
		return nil, fmt.Errorf("unknown PEM type: %s", block.Type)
	}

	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// readCert tries to read and decode the contents of an RSA-signed cert at fn.
// Returns the x509v3 cert, or error otherwise.
func readCert(fn string) (*x509.Certificate, error) {
	b, err := ioutil.ReadFile(fn)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(b)
	switch {
	case block == nil:
		return nil, fmt.Errorf("failed to read cert block")
	case block.Type != "CERTIFICATE":
		return nil, fmt.Errorf("unknown PEM type: %s", block.Type)
	}

	return x509.ParseCertificate(block.Bytes)
}

// createCAPair creates a CA certificate and key pair. The key file is created only
// if it doesn't already exist or we force it. The key path can differ from the certsDir
// which case the path must already exist and be writable.
// Returns nil on success, or an error otherwise.
func createCAPair(opt options) error {
	cc := certConfig{
		isCA:    true,
		until:   opt.days,
		keySize: opt.keySize,
		force:   (opt.force & forceCA) != 0,
	}

	return cc.generatePair(opt.caKey, opt.caCert)
}

// createNodePair creates a node certificate and key pair. The key file is created only
// if it doesn't already exist or we force it. The key path can differ from the certsDir
// which case the path must already exist and be writable.
// Returns nil on success, or an error otherwise.
func createNodePair(opt options) error {
	var err error

	if opt.nodes == nil || len(opt.nodes) == 0 {
		return nil
	}

	cc := certConfig{
		until:   opt.days,
		keySize: opt.keySize,
		force:   (opt.force & forceNode) != 0,
		hosts:   opt.nodes,
	}

	cc.parent, err = readCert(opt.caCert)
	if err != nil {
		return err
	}

	cc.signer, err = readKey(opt.caKey)
	if err != nil {
		return err
	}

	return cc.generatePair(
		filepath.Join(opt.dir, defaultNodeKey),
		filepath.Join(opt.dir, defaultNodeCert),
	)
}

// createClientPair creates a client certificate and key pair. The key file is created only
// if it doesn't already exist or we force it. The key path can differ from the certsDir
// which case the path must already exist and be writable.
// Returns nil on success, or an error otherwise.
func createClientPair(opt options) error {
	var err error

	if opt.user == "" {
		return nil
	}

	cc := certConfig{
		until:   opt.days,
		keySize: opt.keySize,
		force:   (opt.force & forceClient) != 0,
		user:    opt.user,
	}

	cc.parent, err = readCert(opt.caCert)
	if err != nil {
		return err
	}

	cc.signer, err = readKey(opt.caKey)
	if err != nil {
		return err
	}

	return cc.generatePair(
		filepath.Join(opt.dir, fmt.Sprint("client.", opt.user, ".key")),
		filepath.Join(opt.dir, fmt.Sprint("client.", opt.user, ".crt")),
	)
}

func createCerts(opt options) error {
	var err error

	if opt.dir == "" {
		return errors.New("invalid TLS directory")
	}

	err = os.Mkdir(opt.dir, 0700)
	switch {
	case err == nil:
		fmt.Printf("Created new dir: %s\n", opt.dir)
	case os.IsExist(err):
		fmt.Printf("Using existing dir: %s\n", opt.dir)
	default:
		return err
	}

	switch {
	case opt.keySize < keySizeTooSmall:
		return errors.New("key size value is too small (x < 512)")
	case opt.keySize > keySizeTooLarge:
		return errors.New("key size value is too large (x > 4096)")
	case opt.keySize%2 != 0:
		return errors.New("key size value must be a factor of 2")
	}

	// no path then save it in certsDir.
	if path.Base(opt.caKey) == opt.caKey {
		opt.caKey = filepath.Join(opt.dir, opt.caKey)
	}

	opt.caCert = filepath.Join(opt.dir, defaultCACert)

	if err := createCAPair(opt); err != nil {
		return err
	}
	if err := createNodePair(opt); err != nil {
		return err
	}
	if err := createClientPair(opt); err != nil {
		return err
	}

	return nil
}
