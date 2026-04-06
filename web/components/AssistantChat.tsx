"use client";

import { useRef, useState } from "react";

import { api } from "../lib/api";

type Message = {
  role: "user" | "assistant";
  text: string;
};

export function AssistantChat() {
  const [message, setMessage] = useState("");
  const [history, setHistory] = useState<Message[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);

  async function sendMessage() {
    const trimmed = message.trim();
    if (!trimmed || loading) return;
    setError("");
    setHistory((prev) => [...prev, { role: "user", text: trimmed }]);
    setMessage("");
    setLoading(true);
    try {
      const reply = await api.assistant.converse(trimmed);
      setHistory((prev) => [...prev, { role: "assistant", text: reply.Message }]);
      setTimeout(() => bottomRef.current?.scrollIntoView({ behavior: "smooth" }), 50);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to contact assistant");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex flex-col h-[600px] card overflow-hidden">
      {/* Messages */}
      <div className="flex-1 overflow-y-auto px-4 py-4 space-y-3">
        {history.length === 0 ? (
          <div className="h-full flex flex-col items-center justify-center text-center">
            <p className="text-gray-400 text-sm max-w-xs">
              Tell me what you&apos;re looking for. For example: &ldquo;I want a Sony A6700 under €900 in good condition.&rdquo;
            </p>
          </div>
        ) : (
          history.map((item, index) => (
            <div
              key={index}
              className={`flex ${item.role === "user" ? "justify-end" : "justify-start"}`}
            >
              <div
                className={`max-w-[80%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed ${
                  item.role === "user"
                    ? "bg-brand-600 text-white rounded-br-sm"
                    : "bg-gray-100 text-gray-800 rounded-bl-sm"
                }`}
              >
                {item.text}
              </div>
            </div>
          ))
        )}
        {loading && (
          <div className="flex justify-start">
            <div className="bg-gray-100 rounded-2xl rounded-bl-sm px-4 py-2.5">
              <span className="text-gray-400 text-sm">Thinking…</span>
            </div>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {error && (
        <div className="px-4 py-2 border-t border-gray-100">
          <p className="error-msg text-xs">{error}</p>
        </div>
      )}

      {/* Input */}
      <div className="border-t border-gray-200 px-4 py-3 flex gap-2">
        <input
          className="input flex-1"
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Ask the shopping assistant…"
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void sendMessage();
            }
          }}
          disabled={loading}
        />
        <button
          type="button"
          className="btn-primary"
          onClick={() => void sendMessage()}
          disabled={loading || !message.trim()}
        >
          Send
        </button>
      </div>
    </div>
  );
}
