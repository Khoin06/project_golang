package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/kataras/iris/v12"
	"github.com/russross/blackfriday/v2"
)

type GroqRequest struct {
	Prompt string `json:"prompt"`
}

func callGroqAPI(prompt string) (string, error) {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("GROQ_API_KEY chưa được thiết lập")
	}

	url := "https://api.groq.com/openai/v1/chat/completions"
	payload := map[string]interface{}{
		"model":    "mixtral-8x7b-32768",
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Groq API lỗi %d: %s", resp.StatusCode, string(body))
	}

	var groqResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &groqResp); err != nil {
		return "", err
	}

	if len(groqResp.Choices) > 0 {
		return groqResp.Choices[0].Message.Content, nil
	}

	return "No response from Groq", nil
}

func main() {
	app := iris.New()

	app.Get("/", func(ctx iris.Context) {
		if err := ctx.View("index.html"); err != nil {
			ctx.StatusCode(iris.StatusInternalServerError)
			if _, writeErr := ctx.WriteString("Lỗi khi render trang index: " + err.Error()); writeErr != nil {
				ctx.Application().Logger().Error("Lỗi khi ghi phản hồi: ", writeErr)
			}
		}
	})
	app.Get("/favicon.ico", func(ctx iris.Context) {
		ctx.StatusCode(204) // Trả về mã 204 (No Content)
	})
	
	app.Post("/generate", func(ctx iris.Context) {
		var req GroqRequest
		if err := ctx.ReadJSON(&req); err != nil {
			ctx.StatusCode(iris.StatusBadRequest)
			if err := ctx.JSON(iris.Map{"error": "Invalid request"}); err != nil {
				ctx.Application().Logger().Error("Lỗi khi trả về JSON: ", err)
			}
			return
		}

		responseText, err := callGroqAPI(req.Prompt)
		if err != nil {
			ctx.StatusCode(iris.StatusInternalServerError)
			if err := ctx.JSON(iris.Map{"error": err.Error()}); err != nil {
				ctx.Application().Logger().Error("Lỗi khi trả về JSON: ", err)
			}
			return
		}

		htmlContent := string(blackfriday.Run([]byte(responseText)))
		if err := ctx.JSON(iris.Map{"response": htmlContent}); err != nil {
			ctx.Application().Logger().Error("Lỗi khi trả về JSON: ", err)
		}
	})

	tmpl := iris.HTML("views", ".html").Reload(true)
	app.RegisterView(tmpl)

	app.HandleDir("/static", iris.Dir("./static"))

	if err := app.Listen(":8080"); err != nil {
		app.Logger().Fatalf("Lỗi khi khởi động server: %v", err)
	}
	
}