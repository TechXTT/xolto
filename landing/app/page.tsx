"use client";

import { useEffect } from "react";

const APP_URL = process.env.NEXT_PUBLIC_APP_URL || "http://localhost:3000";

function LogoMark() {
  return (
    <div
      className="w-[34px] h-[34px] rounded-[10px] shrink-0 flex items-center justify-center"
      style={{
        background: "linear-gradient(135deg, #0f8f67, #17c88e)",
        boxShadow: "0 6px 18px rgb(15 143 103 / 0.35)",
      }}
    >
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none">
        <path d="M12 2l2.5 6.5L21 11l-6.5 2.5L12 20l-2.5-6.5L3 11l6.5-2.5z" stroke="#fff" strokeWidth="1.8" strokeLinejoin="round" />
      </svg>
    </div>
  );
}

function SectionKicker({ children }: { children: React.ReactNode }) {
  return (
    <div className="inline-flex items-center gap-2 mb-5 text-bright text-xs font-bold tracking-[0.13em] uppercase">
      <span className="w-2 h-2 rounded-full bg-bright" style={{ boxShadow: "0 0 8px #17c88e" }} />
      {children}
    </div>
  );
}

function SectionHeading({ children, className = "" }: { children: React.ReactNode; className?: string }) {
  return (
    <h2 className={`font-serif text-ink leading-[1.1] tracking-[-0.03em] mb-4 ${className}`}
      style={{ fontSize: "clamp(2rem, 4vw, 3rem)" }}>
      {children}
    </h2>
  );
}

export default function LandingPage() {
  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add("visible");
            observer.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.12 }
    );
    document.querySelectorAll(".reveal").forEach((el) => observer.observe(el));
    return () => observer.disconnect();
  }, []);

  return (
    <div className="bg-canvas text-ink font-sans antialiased">

      {/* ── Nav ── */}
      <nav
        className="fixed top-0 left-0 right-0 z-50 flex items-center justify-between gap-4 h-[68px] border-b border-brand/10"
        style={{
          padding: "0 clamp(20px, 4vw, 60px)",
          background: "rgba(8, 15, 12, 0.82)",
          backdropFilter: "blur(18px)",
        }}
      >
        <a href="/" className="flex items-center gap-2.5 font-bold text-[1.05rem] tracking-[-0.02em]">
          <LogoMark />
          <span className="hidden sm:block">xolto</span>
        </a>
        <div className="flex items-center gap-2.5">
          <a href={`${APP_URL}/login`} className="btn btn-ghost btn-sm">Log in</a>
          <a href={`${APP_URL}/register`} className="btn btn-primary btn-sm">Get started free</a>
        </div>
      </nav>

      {/* ── Hero ── */}
      <div
        className="min-h-screen flex items-center pt-[68px]"
        style={{
          background:
            "radial-gradient(ellipse 70% 55% at 65% -5%, rgb(15 143 103 / 0.2), transparent), " +
            "radial-gradient(ellipse 40% 40% at 10% 90%, rgb(15 143 103 / 0.07), transparent)",
        }}
      >
        <div
          className="grid gap-[60px] items-center w-full max-w-[1200px] mx-auto
                     grid-cols-1 lg:grid-cols-2"
          style={{ padding: "clamp(40px, 8vw, 100px) clamp(20px, 4vw, 60px)" }}
        >
          {/* Copy */}
          <div>
            <div className="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full border border-brand/28 bg-brand/10 text-[0.8125rem] font-semibold text-bright mb-7">
              <span className="w-1.5 h-1.5 rounded-full bg-bright animate-glow" />
              AI-powered marketplace intelligence
            </div>

            <h1
              className="font-serif text-ink leading-[0.96] tracking-[-0.04em] mb-5"
              style={{ fontSize: "clamp(3rem, 6.5vw, 5.5rem)" }}
            >
              Buy used electronics<br />
              <em className="not-italic text-bright">without</em><br />
              overpaying.
            </h1>

            <p className="text-ink/60 text-[1.1rem] leading-[1.7] max-w-[50ch] mb-9">
              xolto scans second-hand listings, estimates fair value, flags risks, and tells you exactly which sellers to contact first.
            </p>

            <div className="flex flex-wrap gap-3 mb-10">
              <a href={`${APP_URL}/register`} className="btn btn-primary">Start a buy mission</a>
              <a href={`${APP_URL}/login`} className="btn btn-ghost">Sign in</a>
            </div>

            <div className="flex items-center gap-2.5 text-[0.8125rem] text-ink/38">
              <span>Phones</span>
              <span className="w-1 h-1 rounded-full bg-ink/38" />
              <span>Laptops</span>
              <span className="w-1 h-1 rounded-full bg-ink/38" />
              <span>Cameras</span>
              <span className="w-1 h-1 rounded-full bg-ink/38" />
              <span>and more</span>
            </div>
          </div>

          {/* Deal card mockup */}
          <div
            className="bg-surface rounded-[24px] p-6 border border-brand/28"
            style={{
              boxShadow:
                "0 0 0 1px rgb(15 143 103 / 0.08), 0 40px 80px rgb(0 0 0 / 0.5), inset 0 1px 0 rgb(255 255 255 / 0.04)",
            }}
          >
            <div className="flex items-center justify-between mb-5">
              <div className="flex items-center gap-2 text-[0.8125rem] font-semibold text-ink/60">
                <span className="w-2 h-2 rounded-full bg-bright animate-glow" />
                Live feed · 3 new matches
              </div>
              <span className="text-[0.75rem] font-bold px-2.5 py-1 rounded-full bg-brand/18 text-bright">
                Sony A6700
              </span>
            </div>

            {/* Top deal */}
            <div className="bg-surface2 border border-brand/15 rounded-2xl p-[18px] mb-3.5">
              <div className="flex items-start justify-between gap-2.5 mb-3.5">
                <div>
                  <div className="font-bold text-[0.9375rem] text-ink leading-snug">Sony A6700 Body Only</div>
                  <div className="text-[0.75rem] text-ink/38 mt-1">Like new · Marktplaats</div>
                </div>
                <div className="text-right shrink-0">
                  <div className="text-[1.25rem] font-extrabold text-ink">€840</div>
                  <div className="text-[0.75rem] font-bold text-bright bg-bright/12 px-2.5 py-0.5 rounded-full mt-1 inline-block">
                    Offer €756
                  </div>
                </div>
              </div>
              <div className="h-1.5 rounded-full bg-white/8 overflow-hidden mb-2">
                <div
                  className="h-full rounded-full animate-score-fill"
                  style={{ background: "linear-gradient(90deg, #0f8f67, #17c88e)" }}
                />
              </div>
              <div className="flex justify-between text-[0.75rem]">
                <span className="text-ink/38">AI deal score</span>
                <span className="text-bright font-bold">8.7 · Strong buy</span>
              </div>
            </div>

            {/* Feed rows */}
            <div className="flex flex-col gap-2">
              {[
                { dot: "bg-[#d97706]", name: "Sony A6700 + 18-135mm Kit",  meta: "Good · Vinted · 4 min ago",          price: "€1 040" },
                { dot: "bg-bright",    name: "Sony Alpha A6700 — boxed",    meta: "Like new · Marktplaats · 11 min ago", price: "€870"   },
                { dot: "bg-white/20",  name: "Sony a6700 body (used)",      meta: "Fair · Marktplaats · 22 min ago",    price: "€690"   },
              ].map((row) => (
                <div key={row.name} className="flex items-center gap-3 px-3.5 py-3 bg-surface2 border border-brand/15 rounded-xl">
                  <span className={`w-2 h-2 rounded-full shrink-0 ${row.dot}`} />
                  <div className="flex-1 min-w-0">
                    <div className="text-[0.8125rem] font-semibold text-ink truncate">{row.name}</div>
                    <div className="text-[0.6875rem] text-ink/38">{row.meta}</div>
                  </div>
                  <div className="text-[0.875rem] font-bold text-ink shrink-0">{row.price}</div>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* ── Features ── */}
      <section style={{ padding: "clamp(80px, 10vw, 140px) clamp(20px, 4vw, 60px)" }}>
        <div className="max-w-[1200px] mx-auto">
          <SectionKicker>What it does</SectionKicker>
          <SectionHeading>Your AI buying agent,<br />working while you sleep.</SectionHeading>

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5 mt-14">
            {[
              {
                icon: (
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#17c88e" strokeWidth="1.8" strokeLinecap="round">
                    <circle cx="12" cy="12" r="3" /><path d="M6.3 6.3a8 8 0 0 0 0 11.4" />
                    <path d="M17.7 6.3a8 8 0 0 1 0 11.4" /><path d="M3.5 3.5a14 14 0 0 0 0 17" />
                    <path d="M20.5 3.5a14 14 0 0 1 0 17" />
                  </svg>
                ),
                title: "Live deal radar",
                body: "Set a mission once. xolto polls every marketplace on your behalf and streams new matches to your dashboard — no refreshing, no missed listings.",
              },
              {
                icon: (
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#17c88e" strokeWidth="1.8" strokeLinecap="round">
                    <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" />
                  </svg>
                ),
                title: "AI price intelligence",
                body: "Every listing gets a fair-value score and a suggested offer. Know what to pay and what to skip without hours of cross-referencing sold prices yourself.",
              },
              {
                icon: (
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#17c88e" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M6 3.5h12a.5.5 0 0 1 .5.5v16L12 17l-6.5 3.5V4a.5.5 0 0 1 .5-.5z" />
                  </svg>
                ),
                title: "Shortlist & brief builder",
                body: "Save the listings worth another look. The assistant remembers your preferences and refines your buying brief as you narrow in on what you want.",
              },
            ].map((card, i) => (
              <div
                key={card.title}
                className={`reveal bg-surface border border-brand/15 rounded-[20px] p-7
                            transition-[border-color,box-shadow] duration-200
                            hover:border-brand/28 hover:shadow-[0_16px_40px_rgb(0_0_0/0.3),0_0_0_1px_rgb(15_143_103/0.1)]
                            ${i === 1 ? "reveal-2" : i === 2 ? "reveal-3" : ""}`}
              >
                <div className="w-[50px] h-[50px] rounded-[14px] bg-brand/14 flex items-center justify-center mb-5">
                  {card.icon}
                </div>
                <h3 className="font-bold text-[1.1rem] text-ink mb-2.5 leading-snug">{card.title}</h3>
                <p className="text-ink/60 text-[0.9375rem] leading-[1.65]">{card.body}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── How it works ── */}
      <div className="border-y border-brand/15" style={{ background: "radial-gradient(ellipse 60% 80% at 50% 50%, rgb(15 143 103 / 0.06), transparent)" }}>
        <section style={{ padding: "clamp(80px, 10vw, 140px) clamp(20px, 4vw, 60px)" }}>
          <div className="max-w-[1200px] mx-auto">
            <SectionKicker>How it works</SectionKicker>
            <SectionHeading>From setup to deal alert<br />in three steps.</SectionHeading>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-0 mt-14 relative">
              {/* Connector line (desktop only) */}
              <div
                className="absolute top-12 hidden lg:block border-t border-dashed border-brand/30"
                style={{ left: "calc(33.33% + 20px)", right: "calc(33.33% + 20px)" }}
              />

              {[
                { num: "01", title: "Brief the AI",            body: "Chat with the assistant to describe the item, budget, and condition you want. It extracts your intent and generates a precise set of market hunts — ready to activate." },
                { num: "02", title: "Let it hunt",             body: "Active hunts run on a schedule you set — from every 30 minutes down to every minute. Marktplaats, Vinted, OLX Bulgaria, and more are checked automatically." },
                { num: "03", title: "Act on what surfaces",    body: "Every incoming listing is AI-scored against fair market value. Review the shortlist, see the suggested offer, and message the seller with confidence." },
              ].map((step, i) => (
                <div
                  key={step.num}
                  className={`reveal border-l-2 border-brand/20 pl-6 lg:border-l-0 lg:pl-0
                              ${i === 1 ? "reveal-2 lg:px-7" : i === 2 ? "reveal-3 lg:pl-7" : "lg:pr-7"}`}
                >
                  <div
                    className="font-serif text-[5rem] leading-none mb-4 italic"
                    style={{ color: "rgb(15 143 103 / 0.18)" }}
                  >
                    {step.num}
                  </div>
                  <h4 className="font-bold text-[1.05rem] text-ink mb-2.5">{step.title}</h4>
                  <p className="text-ink/60 text-[0.9375rem] leading-[1.65]">{step.body}</p>
                </div>
              ))}
            </div>
          </div>
        </section>
      </div>

      {/* ── Pricing ── */}
      <section id="pricing" style={{ padding: "clamp(80px, 10vw, 140px) clamp(20px, 4vw, 60px)" }}>
        <div className="max-w-[1200px] mx-auto">
          <SectionKicker>Pricing</SectionKicker>
          <SectionHeading>Pick a plan.<br />Cancel any time.</SectionHeading>
          <p className="text-ink/60 text-[1.05rem] leading-[1.7] max-w-[56ch]">
            Start free and upgrade as your hunt gets more serious.
          </p>

          <div className="grid grid-cols-1 lg:grid-cols-3 gap-5 mt-14 items-start">
            {[
              {
                name: "Free", price: "€0", featured: false,
                features: ["3 active searches", "30 minute polling"],
                missing: ["AI search generation", "Full assistant access", "Auto-messaging"],
                cta: "Get started free",
              },
              {
                name: "Pro", price: "€9", featured: true,
                features: ["10 active searches", "5 minute polling", "AI search generation", "Full assistant access"],
                missing: ["Auto-messaging"],
                cta: "Upgrade to Pro",
              },
              {
                name: "Power", price: "€29", featured: false,
                features: ["Unlimited missions", "50 active searches", "1 minute polling", "AI search generation", "Full assistant access", "Auto-messaging"],
                missing: [],
                cta: "Get Power",
              },
            ].map((plan, i) => (
              <div
                key={plan.name}
                className={`reveal relative bg-surface rounded-[20px] p-7 flex flex-col gap-5
                            ${i === 1 ? "reveal-2" : i === 2 ? "reveal-3" : ""}
                            ${plan.featured
                              ? "border-[1.5px] border-brand"
                              : "border border-brand/15"
                            }`}
                style={plan.featured ? { boxShadow: "0 0 0 1px rgb(15 143 103 / 0.3), 0 30px 60px rgb(15 143 103 / 0.15)" } : undefined}
              >
                {plan.featured && (
                  <span
                    className="absolute -top-[13px] left-1/2 -translate-x-1/2 text-white text-[0.75rem] font-bold px-3.5 py-1 rounded-full tracking-[0.06em] whitespace-nowrap"
                    style={{ background: "linear-gradient(135deg, #0f8f67, #17c88e)" }}
                  >
                    Most popular
                  </span>
                )}

                <div>
                  <div className="text-[0.8125rem] font-bold tracking-[0.1em] uppercase text-ink/38 mb-2">
                    {plan.name}
                  </div>
                  <div className="flex items-baseline gap-1">
                    <strong className="font-serif text-[3.2rem] leading-none text-ink tracking-[-0.04em]">
                      {plan.price}
                    </strong>
                    <span className="text-[0.875rem] text-ink/38">/month</span>
                  </div>
                </div>

                <ul className="flex flex-col gap-2.5">
                  {plan.features.map((f) => (
                    <li key={f} className="flex items-center gap-2.5 text-[0.9rem] text-ink/60">
                      <span className="w-4 h-4 shrink-0 rounded-full bg-brand/18 flex items-center justify-center">
                        <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
                          <path d="M2 5l1.8 1.8L8 3" stroke="#17c88e" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                        </svg>
                      </span>
                      {f}
                    </li>
                  ))}
                  {plan.missing.map((f) => (
                    <li key={f} className="flex items-center gap-2.5 text-[0.9rem] text-ink/25">
                      <span className="w-4 h-4 shrink-0 rounded-full bg-white/4 flex items-center justify-center">
                        <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
                          <path d="M3 5h4" stroke="rgb(240 250 246 / 0.2)" strokeWidth="1.5" strokeLinecap="round" />
                        </svg>
                      </span>
                      {f}
                    </li>
                  ))}
                </ul>

                <a
                  href={`${APP_URL}/register`}
                  className={`btn btn-full ${plan.featured ? "btn-primary" : "btn-ghost"}`}
                >
                  {plan.cta}
                </a>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── Footer ── */}
      <div className="border-t border-brand/15 bg-canvas/70">
        <footer
          className="grid grid-cols-1 lg:grid-cols-3 gap-10 items-center max-w-[1200px] mx-auto"
          style={{ padding: "clamp(40px, 5vw, 60px) clamp(20px, 4vw, 60px)" }}
        >
          <div>
            <a href="/" className="flex items-center gap-2.5 font-bold text-[1.05rem] tracking-[-0.02em]">
              <LogoMark />
              <span>xolto</span>
            </a>
            <p className="text-[0.875rem] text-ink/38 mt-2 max-w-[32ch] leading-[1.55]">
              AI-powered deal intelligence for serious marketplace buyers.
            </p>
          </div>

          <nav className="flex flex-wrap justify-start lg:justify-center gap-7">
            {[
              ["Missions",   `${APP_URL}/missions`],
              ["Matches",    `${APP_URL}/matches`],
              ["Saved",      `${APP_URL}/saved`],
              ["Settings",   `${APP_URL}/settings`],
              ["Pricing",    "#pricing"],
            ].map(([label, href]) => (
              <a key={label} href={href} className="text-[0.875rem] text-ink/38 hover:text-ink transition-colors">
                {label}
              </a>
            ))}
          </nav>

          <div className="flex flex-col items-start lg:items-end gap-3">
            <p className="text-[0.875rem] text-ink/60">Ready to find better deals?</p>
            <a href={`${APP_URL}/register`} className="btn btn-primary btn-sm">
              Create free account
            </a>
          </div>
        </footer>

        <div
          className="text-center text-[0.8rem] text-ink/38 max-w-[1200px] mx-auto py-5"
          style={{ borderTop: "1px solid rgb(15 143 103 / 0.08)", padding: "20px clamp(20px, 4vw, 60px)" }}
        >
          © 2026 xolto &nbsp;·&nbsp; Built for serious electronics buyers
        </div>
      </div>

    </div>
  );
}
