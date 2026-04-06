export function connectDealStream(onMessage: (payload: unknown) => void) {
  const base = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
  const es = new EventSource(`${base}/events`, { withCredentials: true });
  es.onmessage = (event) => {
    try {
      onMessage(JSON.parse(event.data));
    } catch {
      onMessage(event.data);
    }
  };
  return () => es.close();
}
