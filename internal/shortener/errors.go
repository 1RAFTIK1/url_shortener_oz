// Package shortener содержит доменную модель и сценарии сокращателя ссылок
package shortener

import "errors"

// Sentinel-ошибки образуют словарь домена: адаптеры переводят свои сбои в эти
// значения, а транспорт ровно в одном месте отображает их в HTTP-коды
var (
	ErrNotFound           = errors.New("link not found")
	ErrCodeTaken          = errors.New("shortcode is already taken")
	ErrNoFreeCode         = errors.New("no free shortcode available")
	ErrInvalidCode        = errors.New("invalid shortcode")
	ErrInvalidURL         = errors.New("invalid url")
	ErrURLTooLong         = errors.New("url is too long")
	ErrStorageFull        = errors.New("storage is full")
	ErrStorageUnavailable = errors.New("storage is unavailable")
)
