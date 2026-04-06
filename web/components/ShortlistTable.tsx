import { ShortlistEntry } from "../lib/api";

type Props = {
  items: ShortlistEntry[];
  onRemove?: (itemID: string) => void;
};

function euro(cents: number) {
  return cents > 0 ? `€${(cents / 100).toFixed(2)}` : "—";
}

const LABEL_STYLES: Record<string, string> = {
  buy_now:         "badge bg-green-100 text-green-700",
  worth_watching:  "badge bg-yellow-100 text-yellow-700",
  ask_questions:   "badge bg-blue-100 text-blue-700",
  skip:            "badge bg-red-100 text-red-600",
};

export function ShortlistTable({ items, onRemove }: Props) {
  if (items.length === 0) {
    return (
      <div className="card p-8 text-center text-gray-400 text-sm">
        Your shortlist is empty. Save listings from the{" "}
        <a href="/feed" className="underline">Feed</a> to start comparing.
      </div>
    );
  }

  return (
    <div className="card overflow-hidden">
      <table className="w-full text-sm">
        <thead className="bg-gray-50 border-b border-gray-200">
          <tr>
            <th className="text-left px-4 py-3 font-medium text-gray-500 text-xs uppercase tracking-wide">Item</th>
            <th className="text-left px-4 py-3 font-medium text-gray-500 text-xs uppercase tracking-wide">Verdict</th>
            <th className="text-right px-4 py-3 font-medium text-gray-500 text-xs uppercase tracking-wide">Ask</th>
            <th className="text-right px-4 py-3 font-medium text-gray-500 text-xs uppercase tracking-wide">Fair</th>
            <th className="px-4 py-3" />
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {items.map((item) => (
            <tr key={item.ItemID} className="hover:bg-gray-50 transition-colors">
              <td className="px-4 py-3">
                <a
                  href={item.URL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="font-medium text-gray-900 hover:text-brand-600 line-clamp-1"
                >
                  {item.Title}
                </a>
                {item.Concerns?.length > 0 && (
                  <p className="text-xs text-red-500 mt-0.5">{item.Concerns[0]}</p>
                )}
              </td>
              <td className="px-4 py-3">
                {item.RecommendationLabel ? (
                  <span className={LABEL_STYLES[item.RecommendationLabel] ?? "badge bg-gray-100 text-gray-600"}>
                    {item.RecommendationLabel.replace(/_/g, " ")}
                  </span>
                ) : (
                  <span className="text-gray-300">—</span>
                )}
              </td>
              <td className="px-4 py-3 text-right font-mono text-gray-700">{euro(item.AskPrice)}</td>
              <td className="px-4 py-3 text-right font-mono text-gray-500">{euro(item.FairPrice)}</td>
              <td className="px-4 py-3 text-right">
                {onRemove && (
                  <button
                    type="button"
                    className="btn-danger text-xs"
                    onClick={() => onRemove(item.ItemID)}
                  >
                    Remove
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
