// Package i18n writes text in English and Indonesian.
//
// A sentence is a Text: its English template and its parameters. English is
// the template formatted; Indonesian is the same template looked up in a
// Catalog keyed by the English sentence. A service never knows the language:
// it returns a Text, and whoever answers the request renders it.
package i18n
