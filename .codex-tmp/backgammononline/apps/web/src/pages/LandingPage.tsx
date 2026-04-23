import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";

const FEATURES = [
  { icon: "🎲", title: "AI Opponent", desc: "Heuristic AI that plays strategically" },
  { icon: "👥", title: "Local 2-Player", desc: "Pass-and-play with a friend" },
  { icon: "🏆", title: "Tournaments", desc: "Coming soon — real prize pools" },
  { icon: "⚡", title: "Real-Time", desc: "Online multiplayer via Convex" },
];

const STACK = [
  { label: "React + Vite", color: "#61dafb" },
  { label: "Convex", color: "#ff6b35" },
  { label: "Framer Motion", color: "#a855f7" },
  { label: "Tailwind CSS", color: "#38bdf8" },
  { label: "Cloudflare", color: "#f6821f" },
];

// Decorative mini board preview (static SVG-like display)
function MiniBoardPreview() {
  return (
    <motion.div
      animate={{ y: [0, -8, 0] }}
      transition={{ duration: 6, repeat: Infinity, ease: "easeInOut" }}
      className="relative"
      style={{
        width: 320,
        height: 180,
        borderRadius: 14,
        background: "linear-gradient(180deg, #1a0f07 0%, #0d0804 100%)",
        border: "2px solid #3d2a10",
        boxShadow: "0 20px 50px rgba(0,0,0,0.7), 0 0 40px rgba(251,191,36,0.08)",
        overflow: "hidden",
      }}
    >
      {/* Triangles */}
      {Array.from({ length: 12 }).map((_, i) => {
        const isTop = i < 6;
        const isDark = i % 2 === 0;
        return (
          <div
            key={i}
            style={{
              position: "absolute",
              width: `${100 / 13}%`,
              left: `${(i % 6) * (100 / 13)}%`,
              top: isTop ? 0 : "50%",
              height: "44%",
              clipPath: isTop
                ? "polygon(50% 90%, 0% 0%, 100% 0%)"
                : "polygon(50% 10%, 0% 100%, 100% 100%)",
              background: isDark ? "rgba(139,30,30,0.8)" : "rgba(200,140,40,0.7)",
            }}
          />
        );
      })}
      {/* Sample checkers */}
      {[
        { x: "5%", y: "54%", color: "white" },
        { x: "12%", y: "54%", color: "white" },
        { x: "5%", y: "70%", color: "white" },
        { x: "65%", y: "10%", color: "black" },
        { x: "72%", y: "10%", color: "black" },
        { x: "65%", y: "26%", color: "black" },
        { x: "80%", y: "54%", color: "white" },
        { x: "87%", y: "54%", color: "white" },
      ].map((c, i) => (
        <div
          key={i}
          className={c.color === "white" ? "checker-white" : "checker-black"}
          style={{
            position: "absolute",
            left: c.x,
            top: c.y,
            width: 18,
            height: 18,
            borderRadius: "50%",
          }}
        />
      ))}
      {/* Bar */}
      <div
        style={{
          position: "absolute",
          left: "46.2%",
          top: 0,
          bottom: 0,
          width: "7.7%",
          background: "rgba(6,3,1,0.9)",
          borderLeft: "1px solid rgba(255,200,100,0.1)",
          borderRight: "1px solid rgba(255,200,100,0.1)",
        }}
      />
      {/* Glow overlay */}
      <div
        style={{
          position: "absolute",
          inset: 0,
          background: "radial-gradient(ellipse at 50% 50%, rgba(251,191,36,0.05) 0%, transparent 70%)",
          pointerEvents: "none",
        }}
      />
    </motion.div>
  );
}

export default function LandingPage() {
  const navigate = useNavigate();

  return (
    <div
      className="min-h-screen flex flex-col"
      style={{ background: "#050914", minHeight: "100dvh" }}
    >
      {/* Background glow */}
      <div
        className="fixed inset-0 pointer-events-none"
        style={{
          background:
            "radial-gradient(ellipse at 50% 0%, rgba(251,191,36,0.1) 0%, transparent 50%)",
          zIndex: 0,
        }}
      />

      {/* Nav */}
      <motion.nav
        initial={{ opacity: 0, y: -20 }}
        animate={{ opacity: 1, y: 0 }}
        className="relative z-10 flex items-center justify-between px-8 py-5 max-w-6xl mx-auto w-full"
      >
        <div className="flex items-center gap-3">
          <div
            className="w-8 h-8 rounded-xl flex items-center justify-center text-lg"
            style={{ background: "linear-gradient(135deg, #fbbf24, #f59e0b)" }}
          >
            🎲
          </div>
          <span className="font-black text-lg tracking-wide text-gradient-gold">
            BACKGAMMON
          </span>
        </div>
        <div className="flex items-center gap-3">
          <span
            className="text-xs font-mono px-3 py-1 rounded-full"
            style={{
              background: "rgba(251,191,36,0.1)",
              color: "#fbbf24",
              border: "1px solid rgba(251,191,36,0.2)",
            }}
          >
            Phase 1 MVP
          </span>
        </div>
      </motion.nav>

      {/* Hero */}
      <main className="relative z-10 flex-1 flex flex-col items-center justify-center px-6 py-12 text-center gap-12 max-w-5xl mx-auto w-full">
        <div className="flex flex-col lg:flex-row items-center gap-12 w-full">
          {/* Left — text */}
          <motion.div
            initial={{ opacity: 0, x: -40 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ delay: 0.1, type: "spring", stiffness: 120, damping: 20 }}
            className="flex-1 text-left"
          >
            <motion.div
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.2 }}
              className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-xs font-semibold mb-6"
              style={{
                background: "rgba(251,191,36,0.08)",
                color: "#fbbf24",
                border: "1px solid rgba(251,191,36,0.2)",
              }}
            >
              <span className="w-1.5 h-1.5 rounded-full bg-amber-400 animate-pulse" />
              Now live — play instantly, no login required
            </motion.div>

            <h1 className="text-5xl lg:text-6xl font-black leading-tight mb-4">
              <span className="text-white">The world's</span>
              <br />
              <span className="text-gradient-gold">finest backgammon</span>
              <br />
              <span className="text-white">platform</span>
            </h1>

            <p className="text-white/50 text-lg leading-relaxed mb-8 max-w-md">
              Play against a strategic AI or a friend. Built on the Antigravity Stack —
              React, Convex, and Cloudflare — for a game experience without compromise.
            </p>

            <div className="flex flex-wrap gap-3">
              <motion.button
                onClick={() => navigate("/play")}
                whileHover={{ scale: 1.05, y: -2 }}
                whileTap={{ scale: 0.97 }}
                className="px-8 py-4 rounded-2xl font-bold text-base tracking-wide"
                style={{
                  background: "linear-gradient(135deg, #fbbf24 0%, #f59e0b 100%)",
                  color: "#1a0f05",
                  boxShadow: "0 6px 25px rgba(251,191,36,0.45)",
                }}
              >
                🤖 Play vs AI
              </motion.button>
              <motion.button
                onClick={() => navigate("/local")}
                whileHover={{ scale: 1.05, y: -2 }}
                whileTap={{ scale: 0.97 }}
                className="px-8 py-4 rounded-2xl font-bold text-base tracking-wide glass border border-white/10"
                style={{ color: "#e8d9bc" }}
              >
                👥 Local 2-Player
              </motion.button>
            </div>
          </motion.div>

          {/* Right — board preview */}
          <motion.div
            initial={{ opacity: 0, x: 40, rotate: -5 }}
            animate={{ opacity: 1, x: 0, rotate: 0 }}
            transition={{ delay: 0.3, type: "spring", stiffness: 100, damping: 20 }}
            className="flex-shrink-0"
          >
            <MiniBoardPreview />
          </motion.div>
        </div>

        {/* Features */}
        <motion.div
          initial={{ opacity: 0, y: 30 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.5 }}
          className="grid grid-cols-2 lg:grid-cols-4 gap-4 w-full"
        >
          {FEATURES.map((f, i) => (
            <motion.div
              key={f.title}
              initial={{ opacity: 0, y: 20 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.6 + i * 0.08 }}
              whileHover={{ y: -4, scale: 1.02 }}
              className="glass rounded-2xl p-5 text-left cursor-default"
            >
              <div className="text-2xl mb-3">{f.icon}</div>
              <div className="font-semibold text-sm text-white/90 mb-1">{f.title}</div>
              <div className="text-xs text-white/40 leading-relaxed">{f.desc}</div>
            </motion.div>
          ))}
        </motion.div>

        {/* Stack pills */}
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          transition={{ delay: 0.9 }}
          className="flex flex-wrap gap-2 justify-center"
        >
          <span className="text-xs text-white/25 mr-2 self-center">Built with</span>
          {STACK.map(s => (
            <span
              key={s.label}
              className="px-3 py-1 rounded-full text-xs font-mono font-medium"
              style={{
                background: `${s.color}10`,
                color: s.color,
                border: `1px solid ${s.color}25`,
              }}
            >
              {s.label}
            </span>
          ))}
        </motion.div>
      </main>

      {/* Footer */}
      <motion.footer
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ delay: 1 }}
        className="relative z-10 text-center py-6 text-xs text-white/20 font-mono"
      >
        CarrotByte Studios — Antigravity Stack
      </motion.footer>
    </div>
  );
}
