package qqbot

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif"
	"net/http"
	"strings"

	_ "golang.org/x/image/webp"
)

const maxInputImages = 4

// QQ nests quotes, combined messages and parallel text/image parts. Bound the
// traversal independently of the webhook body size and treat all parts as data.
func eventParts(e Event) (string, []Attachment) {
	var text strings.Builder
	attachments := append([]Attachment(nil), e.Attachments...)
	remaining := 32
	var visit func([]MessageElement, int)
	visit = func(elements []MessageElement, depth int) {
		if depth > 4 {
			return
		}
		for _, element := range elements {
			if remaining == 0 {
				return
			}
			remaining--
			if text.Len() < 24000 && element.Content != "" {
				_, _ = text.WriteString(limitText(element.Content, 6000))
				_ = text.WriteByte('\n')
			}
			attachments = append(attachments, element.Attachments...)
			visit(element.MsgElements, depth+1)
		}
	}
	visit(e.MsgElements, 0)
	return limitText(strings.TrimSpace(text.String()), 6000), attachments
}

func imageAttachments(attachments []Attachment) []Attachment {
	var images []Attachment
	for _, attachment := range attachments {
		if strings.HasPrefix(strings.ToLower(attachment.ContentType), "image/") {
			images = append(images, attachment)
		}
	}
	return images
}

func (s *Service) chatMessage(ctx context.Context, e Event, prompt string, ref *botMessageReference) (Message, error) {
	parts, attachments := eventParts(e)
	if parts != "" {
		label := "QQ 消息附带的内容"
		if e.referenceID() != "" || e.MessageType == 103 {
			label = "QQ 消息附带的引用内容"
		}
		prompt = "【" + label + "】\n" + parts + "\n\n【当前消息】\n" + prompt
	}
	if ref != nil {
		quoted := ref.Content
		if ref.Image {
			quoted += "\n（这是一条图片消息；只有本次附带图片时才能读取图片细节。）"
		}
		prompt = "【用户正在回复的机器人消息】\n" + quoted + "\n\n" + prompt
	} else if e.referenceID() != "" && parts == "" {
		prompt = "【引用提示】未找到被引用消息的内容，不要猜测；需要时请用户重新发送。\n\n" + prompt
	}
	message := Message{Role: "user", Content: prompt}
	images := imageAttachments(attachments)
	if len(images) > maxInputImages {
		return Message{}, errors.New("每条消息最多支持 4 张图片，请分开发送。")
	}
	seen := make(map[string]bool)
	total := 0
	for _, attachment := range images {
		if seen[attachment.URL] {
			continue
		}
		seen[attachment.URL] = true
		if attachment.Size > 10<<20 {
			return Message{}, errors.New("单张图片不能超过 10 MB，请压缩后重发。")
		}
		url := attachment.URL
		if strings.HasPrefix(url, "//") {
			url = "https:" + url
		}
		data, err := s.imageDownload(ctx, url)
		if err != nil {
			return Message{}, errors.New("图片读取失败，请重新发送图片后再试。")
		}
		total += len(data)
		if len(data) > 10<<20 || total > 20<<20 {
			return Message{}, errors.New("图片过大，请压缩或分开发送。")
		}
		mime := http.DetectContentType(data)
		switch mime {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return Message{}, errors.New("图片格式不受支持，请发送 JPEG、PNG、GIF 或 WebP 图片。")
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			return Message{}, errors.New("图片尺寸或内容无效，请压缩后重发。")
		}
		message.Images = append(message.Images, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(data))
	}
	return message, nil
}
