interface Props {
  score: number;
  className?: string;
}

export function ScoreBar({ score, className = "" }: Props) {
  const pct = Math.min((score / 10) * 100, 100);
  const color =
    score >= 8 ? "bg-green-500" : score >= 6 ? "bg-yellow-400" : "bg-red-400";

  return (
    <div className={`flex items-center gap-2 ${className}`}>
      <div className="flex-1 h-1.5 bg-gray-100 rounded-full overflow-hidden">
        <div
          className={`h-full rounded-full transition-all ${color}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="text-xs font-mono text-gray-400 w-6 text-right">
        {score.toFixed(1)}
      </span>
    </div>
  );
}
