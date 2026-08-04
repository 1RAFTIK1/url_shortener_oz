package shortener

import "time"

// ровно 10 символов из 63-символьного
// алфавита MaxURLLength значение выбрано мной.
const (
	CodeLength   = 10
	Alphabet     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	MaxURLLength = 2048
)

// Link сохранённое соответствие короткого кода и оригинального адреса
// Ссылка неизменяема ни код, ни адрес после создания не меняются
type Link struct {
	Code      string
	URL       string
	CreatedAt time.Time
}

var allowByte = func() [256]bool {
	var table [256]bool
	for i := 0; i < len(Alphabet); i++ {
		table[Alphabet[i]] = true
	}
	return table
}()

// ValidateCode проверяет, что код синтаксически корректен Вызывается до
// обращения к хранилищу и некорректного кода в базе быть не может по построению.
func ValidateCode(code string) error {
	if len(code) != CodeLength {
		return ErrInvalidCode
	}
	for i := 0; i < len(code); i++ {
		if !allowByte[code[i]] {
			return ErrInvalidCode
		}
	}
	return nil
}
