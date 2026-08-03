package shortener

import "time"

const(
	CodeLenght = 10
	Alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	MaxURLLength = 2048
)

type Link struct {
	Code string
	URL string
	CreatedAt time.Time
}

var allowByte = func() [256]bool{
	var table [256]bool
	for i := 0; i < len(Alphabet); i++ {
		table[Alphabet[i]] = true
	}
	return table
}()

func ValidateCode(code string) error{
	if len(code) != CodeLenght {
		return ErrInvalidCode
	}
	for i := 0; i < len(code); i++ {
		if !allowByte[code[i]] {
			return ErrInvalidCode
		}
	}
	return nil
}