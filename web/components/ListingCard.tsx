import { Listing } from "../lib/api";
import { ScoreBar } from "./ScoreBar";

interface ScoredListing {
  Listing: Listing;
  Score: number;
  OfferPrice: number;
  FairPrice: number;
  Reason?: string;
}

interface Props {
  listing: Listing | ScoredListing;
  onShortlist?: (itemID: string) => Promise<void>;
}

function isScoredListing(l: Listing | ScoredListing): l is ScoredListing {
  return "Listing" in l && "Score" in l;
}

function euro(cents: number) {
  return `€${(cents / 100).toFixed(2)}`;
}

export function ListingCard({ listing, onShortlist }: Props) {
  const l: Listing = isScoredListing(listing) ? listing.Listing : listing;
  const score: number | undefined = isScoredListing(listing) ? listing.Score : undefined;
  const offerPrice: number | undefined = isScoredListing(listing) ? listing.OfferPrice : undefined;
  const fairPrice: number | undefined = isScoredListing(listing) ? listing.FairPrice : undefined;
  const reason: string | undefined = isScoredListing(listing) ? listing.Reason : undefined;

  return (
    <div className="card p-4 flex gap-4 hover:shadow-md transition-shadow">
      {l.ImageURLs && l.ImageURLs.length > 0 ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={l.ImageURLs[0]}
          alt={l.Title}
          className="w-20 h-20 object-cover rounded-md shrink-0 bg-gray-100"
        />
      ) : (
        <div className="w-20 h-20 rounded-md bg-gray-100 shrink-0 flex items-center justify-center">
          <span className="text-gray-300 text-xs">No image</span>
        </div>
      )}

      <div className="flex-1 min-w-0">
        <div className="flex items-start justify-between gap-2">
          <a
            href={l.URL ?? "#"}
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium text-sm text-gray-900 hover:text-brand-600 line-clamp-2 leading-snug"
          >
            {l.Title}
          </a>
          {onShortlist && (
            <button
              type="button"
              onClick={() => onShortlist(l.ItemID)}
              className="btn-secondary text-xs shrink-0"
            >
              + Save
            </button>
          )}
        </div>

        <div className="flex flex-wrap gap-3 mt-1.5 text-sm">
          <span className="font-semibold text-gray-900">{euro(l.Price)}</span>
          {offerPrice ? (
            <span className="text-green-700 font-medium">offer {euro(offerPrice)}</span>
          ) : null}
          {fairPrice ? (
            <span className="text-gray-400">fair {euro(fairPrice)}</span>
          ) : null}
          {l.Condition ? (
            <span className="badge bg-gray-100 text-gray-600">{l.Condition}</span>
          ) : null}
        </div>

        {score !== undefined && <ScoreBar score={score} className="mt-2" />}

        {reason && (
          <p className="text-xs text-gray-400 mt-1 line-clamp-1">{reason}</p>
        )}
      </div>
    </div>
  );
}
