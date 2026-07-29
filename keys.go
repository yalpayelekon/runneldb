package runneldb

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const reservedPrefix = "__rdb__/"

func isReservedKey(key string) bool {
	return strings.HasPrefix(key, reservedPrefix)
}

func catalogTableKey(name string) string {
	return reservedPrefix + "meta/tables/" + name
}

func catalogTablePrefix() string {
	return reservedPrefix + "meta/tables/"
}

func rowKey(table, pkEncoded string) string {
	return reservedPrefix + "t/" + table + "/r/" + pkEncoded
}

func rowKeyPrefix(table string) string {
	return reservedPrefix + "t/" + table + "/r/"
}

func encodePKString(s string) string {
	return "s:" + url.PathEscape(s)
}

func encodePKInt(v int64) string {
	return "i:" + strconv.FormatInt(v, 10)
}

func decodePK(encoded string) (isInt bool, i int64, s string, err error) {
	switch {
	case strings.HasPrefix(encoded, "i:"):
		v, e := strconv.ParseInt(encoded[2:], 10, 64)
		return true, v, "", e
	case strings.HasPrefix(encoded, "s:"):
		raw, e := url.PathUnescape(encoded[2:])
		return false, 0, raw, e
	default:
		return false, 0, "", fmt.Errorf("%w: bad primary key encoding", ErrSQL)
	}
}

func parseRowKey(table, key string) (pkEncoded string, ok bool) {
	prefix := rowKeyPrefix(table)
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	return key[len(prefix):], true
}
