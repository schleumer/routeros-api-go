package routeros

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// A reply can contain multiple pairs. A pair is a string key->value.
// A reply can also contain subpairs, that is, a array of pair arrays.
type Reply struct {
	Pairs    []Pair
	SubPairs []map[string]string
}

func (r *Reply) GetPairVal(key string) (string, error) {
	for _, p := range r.Pairs {
		if p.Key == key {
			return p.Value, nil
		}
	}
	return "", errors.New("key not found")
}

func (r *Reply) GetSubPairByName(key string) (map[string]string, error) {
	for _, p := range r.SubPairs {
		if _, ok := p["name"]; ok {
			if p["name"] == key {
				return p, nil
			}
		}
	}
	return nil, errors.New("key not found")
}

func GetPairVal(pairs []Pair, key string) (string, error) {
	for _, p := range pairs {
		if p.Key == key {
			return p.Value, nil
		}
	}
	return "", errors.New("key not found")
}

// Client is a RouterOS API client.
type Client struct {
	// Network Address.
	// E.g. "10.0.0.1:8728" or "router.example.com:8728"
	address  string
	user     string
	password string
	debug    bool     // debug logging enabled
	ready    bool     // Ready for work (login ok and connection not terminated)
	conn     net.Conn // Connection to pass around
}

// Pair is a Key-Value pair for RouterOS Attribute, Query, and Reply words
// use slices of pairs instead of map because we care about order
type Pair struct {
	Key   string
	Value string
	// Op is used for Query words to signify logical operations
	// valid operators are -, =, <, >
	// see http://wiki.mikrotik.com/wiki/Manual:API#Queries for details.
	Op string
}

type IteratorItem []map[string]string
type PairIterator func(item IteratorItem, err error)

type Query struct {
	Pairs    []Pair
	Op       string
	Proplist []string
}

func NewPair(key string, value string) *Pair {
	p := new(Pair)
	p.Key = key
	p.Value = value
	return p
}

// Create a new instance of the RouterOS API client
func New(address string) (*Client, error) {
	// basic validation of host address
	_, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	var c Client
	c.address = address

	return &c, nil
}

func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) Connect(user string, password string) error {
	conn, err := net.Dial("tcp", c.address)
	if err != nil {
		return err
	}

	// stash conn in instance
	c.conn = conn

	// try to log in
	res, err := c.Call("/login", nil)
	if err != nil {
		return err
	}

	// handle challenge/response
	challengeEnc, err := res.GetPairVal("ret")
	if err != nil {
		return errors.New("Didn't get challenge from ROS")
	}
	challenge, err := hex.DecodeString(challengeEnc)
	if err != nil {
		return err
	}
	h := md5.New()
	io.WriteString(h, "\000")
	io.WriteString(h, password)
	h.Write(challenge)
	resp := fmt.Sprintf("00%x", h.Sum(nil))
	var loginParams []Pair
	loginParams = append(loginParams, *NewPair("name", user))
	loginParams = append(loginParams, *NewPair("response", resp))

	// try to log in again with challenge/response
	res, err = c.Call("/login", loginParams)
	if err != nil {
		return err
	}

	if len(res.Pairs) > 0 {
		return fmt.Errorf("Unexpected result on login: %+v", res)
	}

	return nil
}

func (c *Client) Query(command string, q Query) (Reply, error) {
	err := c.send(command)
	if err != nil {
		return Reply{}, err
	}

	// Set property list if present
	if len(q.Proplist) > 0 {
		proplist := fmt.Sprintf("=.proplist=%s", strings.Join(q.Proplist, ","))
		err = c.send(proplist)
		if err != nil {
			return Reply{}, err
		}
	}

	// send params if we got them
	if len(q.Pairs) > 0 {
		for _, v := range q.Pairs {
			word := fmt.Sprintf("?%s%s=%s", v.Op, v.Key, v.Value)
			c.send(word)
		}

		if q.Op != "" {
			word := fmt.Sprintf("?#%s", q.Op)
			c.send(word)
		}
	}

	// send terminator
	err = c.send("")
	if err != nil {
		return Reply{}, err
	}

	res, err := c.receive()
	if err != nil {
		return Reply{}, err
	}

	return res, nil
}

func (c *Client) KeepAliveCall(command string, q Query, iterator PairIterator) error {
	err := c.send(command)
	if err != nil {
		return err
	}

	// Set property list if present
	if len(q.Proplist) > 0 {
		proplist := fmt.Sprintf("=.proplist=%s", strings.Join(q.Proplist, ","))
		err = c.send(proplist)
		if err != nil {
			return err
		}
	}

	// send params if we got them
	if len(q.Pairs) > 0 {
		for _, v := range q.Pairs {
			word := fmt.Sprintf("%s%s=%s", v.Op, v.Key, v.Value)
			c.send(word)
		}
	}

	// send terminator
	err = c.send("")
	if err != nil {
		return err
	}

	err = c.asyncReceive(func(reply Reply, err error) {
		iterator(reply.SubPairs, err)
	})

	if err != nil {
		return err
	}

	return nil
}

func (c *Client) Call(command string, params []Pair) (Reply, error) {
	err := c.send(command)
	if err != nil {
		return Reply{}, err
	}

	// send params if we got them
	if len(params) > 0 {
		for _, v := range params {
			word := fmt.Sprintf("=%s=%s", v.Key, v.Value)
			c.send(word)
		}
	}

	// send terminator
	err = c.send("")
	if err != nil {
		return Reply{}, err
	}

	res, err := c.receive()
	if err != nil {
		return Reply{}, err
	}

	return res, nil
}
