// Package httpapi httpapi/dto.go структуры, которые сериализуются в JSON и обратно
package httpapi

import "time"

// Типы этого файла контракт с внешним миром. Доменные структуры наружу не
// отдаются иначе переименование поля в домене молча сломало бы всех клиентов

// createLinkRequest тело POST /api/v1/links
type createLinkRequest struct {
	URL string `json:"url"`
}

// linkResponse представление ссылки. Отдаём и code, и готовый short_url
type linkResponse struct {
	Code      string    `json:"code"`
	ShortURL  string    `json:"short_url"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// errorResponse — единый конверт ошибки для всех эндпоинтов
type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	// Code — стабильный машиночитаемый идентификатор
	Code    string `json:"code"`
	Message string `json:"message"`
}
