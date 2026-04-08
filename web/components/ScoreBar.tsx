type Props = {
  score: number;
  className?: string;
};

export function ScoreBar({ score, className = "" }: Props) {
  const pct = Math.min((score / 10) * 100, 100);

  const color =
    score >= 8
      ? { bar: "var(--brand-600)", barEnd: "#17b885", text: "var(--brand-700)", bg: "var(--brand-100)", border: "var(--border-brand)" }
      : score >= 6
        ? { bar: "#d97706", barEnd: "var(--warning-500)", text: "var(--warning-500)", bg: "rgba(245,158,11,0.10)", border: "rgba(245,158,11,0.22)" }
        : { bar: "var(--danger-600)", barEnd: "#ef4444", text: "var(--danger-600)", bg: "rgba(220,38,38,0.08)", border: "rgba(220,38,38,0.18)" };

  const label = score >= 8 ? "Strong deal" : score >= 6 ? "Decent" : "Weak";

  return (
    <div
      className={className}
      style={{ display: "flex", alignItems: "center", gap: "10px" }}
    >
      <div
        style={{
          flex: 1,
          height: "5px",
          background: "rgba(10,26,18,0.07)",
          borderRadius: "9999px",
          overflow: "hidden",
        }}
      >
        <div
          style={{
            height: "100%",
            width: `${pct}%`,
            background: `linear-gradient(90deg, ${color.bar}, ${color.barEnd})`,
            borderRadius: "9999px",
            transition: "width 500ms cubic-bezier(0.25, 0.46, 0.45, 0.94)",
          }}
        />
      </div>
      <span
        style={{
          fontSize: "0.7rem",
          fontWeight: 800,
          color: color.text,
          background: color.bg,
          border: `1px solid ${color.border}`,
          padding: "3px 9px",
          borderRadius: "9999px",
          whiteSpace: "nowrap",
          fontVariantNumeric: "tabular-nums",
          letterSpacing: "0.01em",
        }}
      >
        {score.toFixed(1)} · {label}
      </span>
    </div>
  );
}
