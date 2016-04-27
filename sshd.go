package main

import (
	"fmt"
	"io/ioutil"
	"log"

	"golang.org/x/crypto/ssh"
	"net"
	"errors"
	"strings"
	"os/exec"
	"bytes"
	"encoding/binary"
)

const (
	ENABLE_PASS_AUTH = false
)

var (
	hostKeySigner ssh.Signer
	publicKeys [][]byte
)

func init() {
	pemBytes, err := ioutil.ReadFile("id_rsa")
	if err != nil {
		log.Fatal("Failed to load private key:", err)
	}

	hostKeySigner, err = ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		log.Fatal("Failed to parse private key:", err)
	}

	pemBytes, err = ioutil.ReadFile("authorized_keys")
	if err != nil {
		log.Fatal("Failed to load authorized_key", err)
	}
	publicKeys = make([][]byte, 0)
	for {
		pubKey, _, _, rest, err := ssh.ParseAuthorizedKey(pemBytes)
		if err != nil {
			break
		}
		publicKeys = append(publicKeys, pubKey.Marshal())
		pemBytes = rest
	}
}

func passAuth(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
	fmt.Println("passwordcallback", pass)
	if string(pass) == "tiger" {
		return &ssh.Permissions{}, nil
	}
	return nil, errors.New("fuck you")
}
func pubkeyAuth(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	for _, publicKey := range publicKeys {
		if bytes.Equal(key.Marshal(), publicKey) {
			return nil, nil
		}
	}

	return nil, errors.New("pubkey rejected")
}

func parseArgs(str string) (command string, args []string) {
	commandSlice := strings.SplitN(str, " ", 2)
	command = commandSlice[0]

	argsEscapedSlice := strings.Split(commandSlice[1], "'")
	parity := false

	fmt.Println(argsEscapedSlice, strings.Join(argsEscapedSlice, ","))

	for _, elem := range argsEscapedSlice {
		if parity {
			args = append(args, elem)
		} else {
			args = append(args, strings.Split(strings.TrimSpace(elem), " ")...)
		}
		parity = !parity
	}

	return
}

// Payload: int: command size, string: command
func handleExec(ch ssh.Channel, req *ssh.Request) {

	length := binary.BigEndian.Uint32(req.Payload)
	fmt.Println("command length", length)
	//todo use length subslice and range check

	command := string(req.Payload[4:])
	if strings.HasPrefix(command, "git") {
		fmt.Println("running", command)

		command, args := parseArgs(command)
		fmt.Printf("cmd:%s args:%v\n", command, strings.Join(args, ","))
		cmd := exec.Command(command, args...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		if err != nil {
			fmt.Println("there was an error running cmd:", err.Error())
		}
		fmt.Println("out", out.Bytes())

		ch.Write(out.Bytes())
		ch.Close()
	} else {
		ch.Write([]byte("command is not a GIT command\r\n"))
		ch.Close()
		return
	}
}

func handleChan(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		if req.Type == "env" {
			ptr := req.Payload
			//todo range check
			length := binary.BigEndian.Uint32(ptr)

			name := string(ptr[4:4+length])
			ptr = ptr[4+length:]
			length = binary.BigEndian.Uint32(ptr)

			value := string(ptr[4:4+length])
			fmt.Println("ENV", name, value)
			continue
		}
		if req.Type != "exec" {
			fmt.Println("skipping request", req)
			continue
		}
		handleExec(ch, req)
	}
}

func chanReq(chanReq ssh.NewChannel) {
	if chanReq.ChannelType() != "session" {
		chanReq.Reject(ssh.Prohibited, "channel type is not a session")
		return
	}
	ch, reqs, err := chanReq.Accept()
	if err != nil {
		log.Fatal(err)
	}
	go handleChan(ch, reqs)
}

func handleSshClientConnection(sshConn *ssh.ServerConn, inChans <-chan ssh.NewChannel) {
	defer sshConn.Close()
	for req := range inChans {
		go chanReq(req)
	}
}

func main() {
	config := &ssh.ServerConfig{
		PublicKeyCallback: pubkeyAuth,
	}
	if ENABLE_PASS_AUTH {
		config.PasswordCallback = passAuth
	}

	config.AddHostKey(hostKeySigner)
	address := ":2022"
	serverConn, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatal("Could not listen on address:", address)
		panic(err)
	}
	for {
		log.Println("Accepting")
		clientConn, err := serverConn.Accept()
		if err != nil {
			panic(err)
		}
		log.Println("Client connected from", clientConn.RemoteAddr())

		sshConn, inChans, _, err := ssh.NewServerConn(clientConn, config)
		if err != nil {
			log.Println("Failed to create ssh connection")
			clientConn.Close()
			continue
		}
		go handleSshClientConnection(sshConn, inChans)
	}
}

