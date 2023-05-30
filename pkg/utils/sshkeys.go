package utils

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	ecnsv1 "easystack.com/plan/api/v1"
	"encoding/pem"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io/ioutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

func MakeSSHKeyPair() (string, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return "", "", err
	}

	// generate and write private key as PEM
	var privKeyBuf strings.Builder

	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}
	if err := pem.Encode(&privKeyBuf, privateKeyPEM); err != nil {
		return "", "", err
	}

	// generate and write public key
	pub, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}

	var pubKeyBuf strings.Builder
	pubKeyBuf.Write(ssh.MarshalAuthorizedKey(pub))

	return pubKeyBuf.String(), privKeyBuf.String(), nil
}

func GetOrCreateSSHKeySecret(ctx context.Context, client client.Client, plan *ecnsv1.Plan) (string, string, error) {
	secretName := plan.Name + "-default-ssh"
	//get secret by name secretName
	secret := &corev1.Secret{}
	err := client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: plan.Namespace}, secret)
	if err != nil {
		// if err is not found, create secret
		if errors.IsNotFound(err) {
			pub, pri, err := MakeSSHKeyPair()
			if err != nil {
				return "", "", err
			}
			secret.Namespace = plan.Namespace
			secret.Data = map[string][]byte{
				"public_key":  []byte(pub),
				"private_key": []byte(pri),
			}
			err = client.Create(ctx, secret)
			if err != nil {
				return pub, pri, err
			}
			return pub, pri, nil
		}
		return "", "", err
	}
	// if secret is found, return public key and private key
	pub := string(secret.Data["public_key"])
	pri := string(secret.Data["private_key"])
	return pub, pri, nil
}

// GetOrCreateSSHkeyFile  create private key file
func GetOrCreateSSHkeyFile(ctx context.Context, cli client.Client, plan *ecnsv1.Plan) error {
	path := fmt.Sprintf("/root/.ssh/id_rsa_%s", plan.Spec.ClusterName)
	// judge if path of file exists
	if FileExist(path) {
		return nil
	}
	// get public key and private key
	_, pri, err := GetOrCreateSSHKeySecret(context.Background(), cli, plan)
	if err != nil {
		return err
	}
	// create file
	err = ioutil.WriteFile(path, []byte(pri), 0600)
	if err != nil {
		return err
	}
	return nil
}

func FileExist(path string) bool {
	_, err := os.Lstat(path)
	return !os.IsNotExist(err)
}
