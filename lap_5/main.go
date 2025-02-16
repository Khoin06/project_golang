package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	_ "github.com/denisenkom/go-mssqldb"
	"github.com/kataras/iris/v12"
)

type Dialog struct {
	ID      int    `json:"id"`
	Lang    string `json:"lang"`
	Content string `json:"content"`
}

var (
	db  *sql.DB
	env struct {
		DBServer   string
		DBUser     string
		DBPassword string
		DBName     string
		GroqAPIKey string
	}
)

func init() {
	env.DBServer = getEnv("DB_SERVER", "localhost")
	env.DBUser = getEnv("DB_USER", "sa")
	env.DBPassword = getEnv("DB_PASSWORD", "123456")
	env.DBName = getEnv("DB_NAME", "DialogDB")
	env.GroqAPIKey = os.Getenv("GROQ_API_KEY")

	if env.GroqAPIKey == "" {
		log.Fatal("GROQ_API_KEY environment variable is required")
	}
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func initDB() {
	var err error
	connStr := fmt.Sprintf("server=%s;user id=%s;password=%s;database=%s",
		env.DBServer, env.DBUser, env.DBPassword, env.DBName)

	db, err = sql.Open("sqlserver", connStr)
	if err != nil {
		log.Fatalf("Database connection error: %v", err)
	}

	if err = db.Ping(); err != nil {
		log.Fatalf("Database ping failed: %v", err)
	}

	log.Println("Successfully connected to database")
}
func saveDialog(content string) (int64, error) {
	var dialogID sql.NullInt64 // Dùng NullInt64 để tránh lỗi NULL
	query := "INSERT INTO dialog (content) VALUES (@Content); SELECT SCOPE_IDENTITY();"

	err := db.QueryRow(query, sql.Named("Content", content)).Scan(&dialogID)
	if err != nil {
		log.Printf("SQL Execution Error: %v", err)
		return 0, fmt.Errorf("failed to insert dialog: %w", err)
	}

	if !dialogID.Valid {
		log.Printf("Warning: SCOPE_IDENTITY() returned NULL")
		return 0, fmt.Errorf("failed to retrieve inserted ID")
	}

	log.Printf("Inserted dialog ID: %d", dialogID.Int64)
	return dialogID.Int64, nil
}





func saveWord(vietnamese, english string) (int64, error) {
	var wordID int64

	// Kiểm tra xem từ đã tồn tại chưa
	query := "SELECT id FROM word WHERE content = @Content"
	err := db.QueryRow(query, sql.Named("Content", vietnamese)).Scan(&wordID)

	if err == sql.ErrNoRows { // Nếu chưa có, tiến hành chèn
		insertQuery := "INSERT INTO word (lang, content, translate) OUTPUT INSERTED.id VALUES (@Lang, @Content, @Translate)"

		err = db.QueryRow(insertQuery,
			sql.Named("Lang", "vi"),
			sql.Named("Content", vietnamese),
			sql.Named("Translate", english),
		).Scan(&wordID)

		if err != nil {
			log.Printf("Lỗi khi thêm từ: %v", err)
			return 0, fmt.Errorf("failed to insert word: %w", err)
		}
	} else if err != nil {
		log.Printf("Lỗi khi kiểm tra từ: %v", err)
		return 0, fmt.Errorf("failed to check word: %w", err)
	}

	log.Printf("Lưu thành công từ ID %d: %s -> %s", wordID, vietnamese, english)
	return wordID, nil
}



func linkWordDialog(dialogID, wordID int64) error {
	query := "INSERT INTO word_dialog (dialog_id, word_id) VALUES (?, ?)"
	_, err := db.Exec(query, dialogID, wordID)
	return err
}

func callGroqAPI(prompt string) (string, error) {
	url := "https://api.groq.com/openai/v1/chat/completions"
	payload := map[string]interface{}{
		"model":    "mixtral-8x7b-32768",
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("JSON marshal error: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("request creation failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.GroqAPIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("response decoding failed: %w", err)
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("empty response from API")
	}

	return response.Choices[0].Message.Content, nil
}

func generativeConversationFromGroq() (string, error) {
	// Yêu cầu Groq chỉ trả về danh sách từ quan trọng dưới dạng JSON
	prompt := fmt.Sprintf(`
	Tạo một hội thoại bằng tiếng Việt, gồm 6 câu, ngắn gọn, đơn giản, 
	hỏi đường đi đến hồ Hoàn Kiếm Hà nội giữa một người Mỹ tên James và 
	người Việt nam tên Lan. Chỉ xuất ra hội thoại không cần giải thích. `)

	responseText, err := callGroqAPI(prompt)
	if err != nil {
		return "", fmt.Errorf("Groq API error: %w", err)
	}

	// Ghi log kiểm tra phản hồi từ Groq
	log.Printf("Groq API raw response: %s", responseText)
	return responseText, err
}

// Di chuyển extractImportantWords ra ngoài
func extractImportantWordsFromGroq(text string) ([]map[string]string, error) {
	// Yêu cầu Groq chỉ trả về danh sách từ quan trọng dưới dạng JSON
	prompt := fmt.Sprintf(`
	Trích xuất các từ quan trọng từ đoạn hội thoại sau và dịch chúng sang tiếng Anh. 
	Trả về JSON dạng: {"words": [{"vietnamese": "từ", "english": "word"}]}.
	Không thêm bất kỳ nội dung nào khác.
	
	Hội thoại:
	%s
	`, text)

	responseText, err := callGroqAPI(prompt)
	if err != nil {
		return nil, fmt.Errorf("Groq API error: %w", err)
	}

	// Ghi log kiểm tra phản hồi từ Groq
	log.Printf("Groq API raw response: %s", responseText)

	// Kiểm tra nếu không phải JSON hợp lệ
	if !json.Valid([]byte(responseText)) {
		return nil, fmt.Errorf("invalid JSON response from Groq API")
	}

	// Giải mã JSON thành struct
	var extracted struct {
		Words []map[string]string `json:"words"`
	}
	if err := json.Unmarshal([]byte(responseText), &extracted); err != nil {
		return nil, fmt.Errorf("JSON decoding error: %w", err)
	}

	return extracted.Words, nil
}

func main() {
	initDB()
	defer db.Close()

	app := iris.New()
	app.Logger().SetLevel("debug")
	app.RegisterView(iris.HTML("./views", ".html"))

	app.Use(func(ctx iris.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Next()
	})

	app.Get("/favicon.ico", func(ctx iris.Context) {
		ctx.StatusCode(204) // Trả về mã 204 (No Content)
	})

	app.Get("/", func(ctx iris.Context) {
		if err := ctx.View("index.html"); err != nil {
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.WriteString("Error loading page")
		}
	})
	app.Post("/generate-conversation-ai", func(ctx iris.Context) {
		var req struct {
			Words []string `json:"words"`
		}

		// Đọc danh sách từ từ request
		if err := ctx.ReadJSON(&req); err != nil {
			ctx.StatusCode(iris.StatusBadRequest)
			ctx.JSON(iris.Map{"error": "Invalid request format"})
			return
		}

		// Chuyển danh sách từ thành prompt cho Groq
		prompt := fmt.Sprintf(`
	Hãy tạo một hội thoại bằng tiếng Việt gồm 6 câu dựa trên danh sách từ sau:
	%s
	Đảm bảo hội thoại tự nhiên và có ngữ cảnh.
	Trả về JSON: {"conversation": [{"name": "James", "text": "Câu thoại"}]}
	`, req.Words)

		responseText, err := callGroqAPI(prompt)
		if err != nil {
			log.Printf("Groq API error: %v", err)
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.JSON(iris.Map{"error": "Failed to generate conversation"})
			return
		}

		// Kiểm tra JSON hợp lệ
		if !json.Valid([]byte(responseText)) {
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.JSON(iris.Map{"error": "Invalid response from AI"})
			return
		}

		// Gửi lại hội thoại cho frontend
		ctx.WriteString(responseText)
	})

	app.Post("/generate", func(ctx iris.Context) {
		var req Dialog
		if err := ctx.ReadJSON(&req); err != nil {
			ctx.StatusCode(iris.StatusBadRequest)
			ctx.JSON(iris.Map{"error": "Invalid request format"})
			return
		}

		// Bước 1: Lưu hội thoại gốc
		originalText, err := generativeConversationFromGroq()

        saveDialog(originalText); 
		

		// Bước 2: Lọc từ quan trọng
		importantWords, err := extractImportantWordsFromGroq(originalText)
		if err != nil {
			log.Printf("Error extracting important words: %v", err)
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.JSON(iris.Map{"error": "Failed to extract important words"})
			return
		}

		// Bước 3: Dịch từ sang tiếng Anh (nếu chưa có)
/*		translatedWords := []map[string]string{}
		for _, word := range importantWords {
			translatedWords = append(translatedWords, map[string]string{ // ✅ Dùng map[string]string thay vì iris.Map
				"vietnamese": word["vietnamese"],
				"english":    word["english"],
			})
		}
*/

translatedWords := []map[string]string{}

for _, word := range importantWords {
    vietnamese := word["vietnamese"]
    english := word["english"]

    // Gọi hàm lưu từ vào DB
    wordID, err := saveWord(vietnamese, english)
    if err != nil {
        log.Printf("Lỗi khi lưu từ '%s': %v", vietnamese, err)
        continue // Nếu lỗi, bỏ qua từ này và tiếp tục
    }

    log.Printf("Lưu thành công từ ID %d: %s -> %s", wordID, vietnamese, english)

    // Thêm từ vào danh sách JSON trả về
    translatedWords = append(translatedWords, map[string]string{
        "id":          fmt.Sprintf("%d", wordID), // Chuyển ID thành string để tránh lỗi kiểu dữ liệu
        "vietnamese":  vietnamese,
        "english":     english,
    })
}

		// Bước 4: Trả về JSON chứa tất cả các bước
		ctx.JSON(iris.Map{
			"original_text": originalText,
			"words":         translatedWords,
		})
	})

	app.Listen(":8080", iris.WithOptimizations)
}
