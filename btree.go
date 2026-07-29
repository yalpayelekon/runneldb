package runneldb

import "fmt"

const btreeOrder = 8 // max keys per node before split (even)

// pkKey is a typed primary key for B+tree ordering.
type pkKey struct {
	isInt bool
	i     int64
	s     string
}

func pkFromInt(v int64) pkKey     { return pkKey{isInt: true, i: v} }
func pkFromString(v string) pkKey { return pkKey{isInt: false, s: v} }

func (a pkKey) compare(b pkKey) int {
	if a.isInt != b.isInt {
		if a.isInt {
			return -1
		}
		return 1
	}
	if a.isInt {
		switch {
		case a.i < b.i:
			return -1
		case a.i > b.i:
			return 1
		default:
			return 0
		}
	}
	switch {
	case a.s < b.s:
		return -1
	case a.s > b.s:
		return 1
	default:
		return 0
	}
}

func (k pkKey) encode() string {
	if k.isInt {
		return encodePKInt(k.i)
	}
	return encodePKString(k.s)
}

type btreeNode struct {
	leaf     bool
	keys     []pkKey
	children []*btreeNode // interior: len = len(keys)+1
	vals     []string     // leaf: row key strings, parallel to keys
	next     *btreeNode   // leaf sibling
}

// btree is an in-memory B+tree mapping primary keys to row storage keys.
type btree struct {
	root *btreeNode
}

func newBTree() *btree {
	return &btree{root: &btreeNode{leaf: true}}
}

func (t *btree) Get(k pkKey) (rowKey string, ok bool) {
	n := t.root
	for !n.leaf {
		i := n.childIndex(k)
		n = n.children[i]
	}
	for i, key := range n.keys {
		if key.compare(k) == 0 {
			return n.vals[i], true
		}
	}
	return "", false
}

func (t *btree) Put(k pkKey, rowKey string) {
	if splitKey, splitRight, grew := t.root.insert(k, rowKey); grew {
		t.root = &btreeNode{
			leaf:     false,
			keys:     []pkKey{splitKey},
			children: []*btreeNode{t.root, splitRight},
		}
	}
}

func (t *btree) Delete(k pkKey) {
	t.root.delete(k)
	if !t.root.leaf && len(t.root.keys) == 0 && len(t.root.children) == 1 {
		t.root = t.root.children[0]
	}
}

func (t *btree) Seek(k pkKey) *btreeIter {
	n := t.root
	for !n.leaf {
		i := n.childIndex(k)
		n = n.children[i]
	}
	idx := 0
	for idx < len(n.keys) && n.keys[idx].compare(k) < 0 {
		idx++
	}
	return &btreeIter{node: n, idx: idx}
}

func (t *btree) Ascend() *btreeIter {
	n := t.root
	for !n.leaf {
		n = n.children[0]
	}
	return &btreeIter{node: n, idx: 0}
}

type btreeIter struct {
	node *btreeNode
	idx  int
}

func (it *btreeIter) Next() (pkKey, string, bool) {
	for it.node != nil {
		if it.idx < len(it.node.keys) {
			k := it.node.keys[it.idx]
			v := it.node.vals[it.idx]
			it.idx++
			return k, v, true
		}
		it.node = it.node.next
		it.idx = 0
	}
	return pkKey{}, "", false
}

func (n *btreeNode) childIndex(k pkKey) int {
	i := 0
	for i < len(n.keys) && k.compare(n.keys[i]) >= 0 {
		i++
	}
	return i
}

func (n *btreeNode) insert(k pkKey, rowKey string) (pkKey, *btreeNode, bool) {
	if n.leaf {
		return n.insertLeaf(k, rowKey)
	}
	i := n.childIndex(k)
	splitKey, splitRight, grew := n.children[i].insert(k, rowKey)
	if !grew {
		return pkKey{}, nil, false
	}
	n.keys = append(n.keys, pkKey{})
	n.children = append(n.children, nil)
	copy(n.keys[i+1:], n.keys[i:])
	copy(n.children[i+2:], n.children[i+1:])
	n.keys[i] = splitKey
	n.children[i+1] = splitRight
	if len(n.keys) <= btreeOrder {
		return pkKey{}, nil, false
	}
	return n.splitInterior()
}

func (n *btreeNode) insertLeaf(k pkKey, rowKey string) (pkKey, *btreeNode, bool) {
	i := 0
	for i < len(n.keys) && n.keys[i].compare(k) < 0 {
		i++
	}
	if i < len(n.keys) && n.keys[i].compare(k) == 0 {
		n.vals[i] = rowKey
		return pkKey{}, nil, false
	}
	n.keys = append(n.keys, pkKey{})
	n.vals = append(n.vals, "")
	copy(n.keys[i+1:], n.keys[i:])
	copy(n.vals[i+1:], n.vals[i:])
	n.keys[i] = k
	n.vals[i] = rowKey
	if len(n.keys) <= btreeOrder {
		return pkKey{}, nil, false
	}
	return n.splitLeaf()
}

func (n *btreeNode) splitLeaf() (pkKey, *btreeNode, bool) {
	mid := len(n.keys) / 2
	right := &btreeNode{
		leaf: true,
		keys: append([]pkKey(nil), n.keys[mid:]...),
		vals: append([]string(nil), n.vals[mid:]...),
		next: n.next,
	}
	n.keys = n.keys[:mid]
	n.vals = n.vals[:mid]
	n.next = right
	return right.keys[0], right, true
}

func (n *btreeNode) splitInterior() (pkKey, *btreeNode, bool) {
	mid := len(n.keys) / 2
	up := n.keys[mid]
	right := &btreeNode{
		leaf:     false,
		keys:     append([]pkKey(nil), n.keys[mid+1:]...),
		children: append([]*btreeNode(nil), n.children[mid+1:]...),
	}
	n.keys = n.keys[:mid]
	n.children = n.children[:mid+1]
	return up, right, true
}

func (n *btreeNode) delete(k pkKey) {
	if n.leaf {
		for i, key := range n.keys {
			if key.compare(k) == 0 {
				n.keys = append(n.keys[:i], n.keys[i+1:]...)
				n.vals = append(n.vals[:i], n.vals[i+1:]...)
				return
			}
		}
		return
	}
	i := n.childIndex(k)
	n.children[i].delete(k)
}

func pkKeyFromEncoded(encoded string) (pkKey, error) {
	isInt, i, s, err := decodePK(encoded)
	if err != nil {
		return pkKey{}, err
	}
	if isInt {
		return pkFromInt(i), nil
	}
	return pkFromString(s), nil
}

func formatPK(k pkKey) string {
	if k.isInt {
		return fmt.Sprintf("%d", k.i)
	}
	return k.s
}
