package meshclaw

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"
)

var webChatHTML = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Name}} - meshclaw</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; background: #1a1a2e; color: #eee; height: 100vh; display: flex; flex-direction: column; }
        .header { padding: 1rem; background: #16213e; border-bottom: 1px solid #0f3460; }
        .header h1 { font-size: 1.2rem; color: #e94560; }
        .chat { flex: 1; overflow-y: auto; padding: 1rem; }
        .message { margin: 0.5rem 0; padding: 0.75rem 1rem; border-radius: 1rem; max-width: 80%; }
        .user { background: #0f3460; margin-left: auto; }
        .assistant { background: #16213e; }
        .input-area { padding: 1rem; background: #16213e; border-top: 1px solid #0f3460; }
        .input-row { display: flex; gap: 0.5rem; }
        input { flex: 1; padding: 0.75rem 1rem; border: none; border-radius: 1.5rem; background: #1a1a2e; color: #eee; font-size: 1rem; }
        button { padding: 0.75rem 1.5rem; border: none; border-radius: 1.5rem; background: #e94560; color: white; font-weight: bold; cursor: pointer; }
        button:hover { background: #ff6b6b; }
        .loading { opacity: 0.5; }
        pre { background: #0f0f1a; padding: 0.5rem; border-radius: 0.5rem; overflow-x: auto; font-size: 0.9rem; }
    </style>
</head>
<body>
    <div class="header"><h1>{{.Name}}</h1></div>
    <div class="chat" id="chat"></div>
    <div class="input-area">
        <div class="input-row">
            <input type="text" id="input" placeholder="Type a message..." autofocus>
            <button onclick="send()">Send</button>
        </div>
    </div>
    <script>
        const chat = document.getElementById('chat');
        const input = document.getElementById('input');

        function addMessage(text, isUser) {
            const div = document.createElement('div');
            div.className = 'message ' + (isUser ? 'user' : 'assistant');
            div.innerHTML = text.replace(/` + "`" + `([^` + "`" + `]+)` + "`" + `/g, '<code>$1</code>')
                               .replace(/\n/g, '<br>');
            chat.appendChild(div);
            chat.scrollTop = chat.scrollHeight;
            return div;
        }

        async function send() {
            const msg = input.value.trim();
            if (!msg) return;

            addMessage(msg, true);
            input.value = '';

            const loading = addMessage('...', false);
            loading.className += ' loading';

            try {
                const res = await fetch('/api/chat', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({message: msg})
                });
                const data = await res.json();
                loading.remove();
                addMessage(data.response || data.error, false);
            } catch (e) {
                loading.remove();
                addMessage('Error: ' + e.message, false);
            }
        }

        input.addEventListener('keypress', e => { if (e.key === 'Enter') send(); });
    </script>
</body>
</html>`

// WebChatServer serves the web chat interface
type WebChatServer struct {
	cfg    *Config
	server *http.Server
	mu     sync.Mutex
}

// NewWebChatServer creates a new web chat server
func NewWebChatServer(cfg *Config) *WebChatServer {
	return &WebChatServer{cfg: cfg}
}

// Start starts the web chat server
func (w *WebChatServer) Start() error {
	port := 8080
	host := "0.0.0.0"
	if w.cfg.WebChat != nil {
		if w.cfg.WebChat.Port > 0 {
			port = w.cfg.WebChat.Port
		}
		if w.cfg.WebChat.Host != "" {
			host = w.cfg.WebChat.Host
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", w.handleIndex)
	mux.HandleFunc("/api/chat", w.handleChat)

	addr := fmt.Sprintf("%s:%d", host, port)
	w.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	fmt.Printf("[%s] WebChat started: http://%s\n", w.cfg.Name, addr)
	return w.server.ListenAndServe()
}

// Stop stops the web chat server
func (w *WebChatServer) Stop() error {
	if w.server != nil {
		return w.server.Close()
	}
	return nil
}

func (w *WebChatServer) handleIndex(rw http.ResponseWriter, r *http.Request) {
	// Check password if configured
	if w.cfg.WebChat != nil && w.cfg.WebChat.Password != "" {
		if r.URL.Query().Get("p") != w.cfg.WebChat.Password {
			http.Error(rw, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	tmpl, err := template.New("chat").Parse(webChatHTML)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/html")
	tmpl.Execute(rw, map[string]string{"Name": w.cfg.Name})
}

func (w *WebChatServer) handleChat(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	w.mu.Lock()
	response := processMessage(w.cfg, req.Message)
	w.mu.Unlock()

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]string{"response": response})
}

// StartWebChat starts webchat if configured
func StartWebChat(cfg *Config, stopCh <-chan struct{}) {
	if cfg.WebChat == nil {
		return
	}

	server := NewWebChatServer(cfg)
	go func() {
		<-stopCh
		server.Stop()
	}()
	server.Start()
}
