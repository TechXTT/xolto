import Link from "next/link";

export default function HomePage() {
  return (
    <main>
      <h1>MarktBot SaaS</h1>
      <p>Use the dashboard to create searches, monitor deals, chat with the assistant, and manage billing.</p>
      <div style={{ display: "flex", gap: 12 }}>
        <Link href="/login">Login</Link>
        <Link href="/register">Register</Link>
        <Link href="/feed">Open dashboard</Link>
      </div>
    </main>
  );
}
