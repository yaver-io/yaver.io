/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      fontFamily: {
        sans: ["'Outfit'", "Inter", "sans-serif"],
        display: ["'Outfit'", "sans-serif"],
        mono: ["'JetBrains Mono'", "monospace"],
      },
      colors: {
        // Deep navy background system
        navy: {
          950: "#050914",
          900: "#080f22",
          800: "#0d1530",
          700: "#111d3d",
          600: "#1a2850",
          500: "#243360",
        },
        // Amber/gold accent — checker and UI highlights
        amber: {
          50:  "#fffbeb",
          100: "#fef3c7",
          200: "#fde68a",
          300: "#fcd34d",
          400: "#fbbf24",
          500: "#f59e0b",
          600: "#d97706",
          700: "#b45309",
          800: "#92400e",
          900: "#78350f",
        },
        // Crimson for black checkers / accents
        crimson: {
          400: "#f87171",
          500: "#ef4444",
          600: "#dc2626",
          700: "#b91c1c",
          800: "#991b1b",
          900: "#7f1d1d",
        },
        // Board triangle colors
        board: {
          dark:  "#1e0a0a",    // Dark triangles
          light: "#1a2a1a",    // Light triangles
          surface: "#120b04",  // Board surface
          border: "#3d2a10",   // Board border/frame
          point: "#2a1508",    // Point background
        },
        // Glass surfaces
        glass: {
          light: "rgba(255, 255, 255, 0.05)",
          medium: "rgba(255, 255, 255, 0.08)",
          heavy: "rgba(255, 255, 255, 0.12)",
          border: "rgba(255, 255, 255, 0.10)",
        },
      },
      backgroundImage: {
        "gradient-radial": "radial-gradient(var(--tw-gradient-stops))",
        "board-felt": "radial-gradient(ellipse at center, #1a0f07 0%, #0d0804 100%)",
        "hero-glow": "radial-gradient(ellipse at 50% 40%, rgba(251,191,36,0.15) 0%, transparent 60%)",
        "checker-white": "radial-gradient(circle at 35% 30%, #ffffff 0%, #d4c5a0 40%, #a08060 100%)",
        "checker-black": "radial-gradient(circle at 35% 30%, #6b3a2a 0%, #3d1f15 40%, #1a0a08 100%)",
      },
      boxShadow: {
        "checker": "0 4px 12px rgba(0,0,0,0.6), inset 0 1px 2px rgba(255,255,255,0.2)",
        "checker-selected": "0 0 0 3px #fbbf24, 0 4px 20px rgba(251,191,36,0.5)",
        "checker-legal": "0 0 0 2px rgba(251,191,36,0.6), 0 0 15px rgba(251,191,36,0.3)",
        "dice": "0 8px 24px rgba(0,0,0,0.5), inset 0 1px 0 rgba(255,255,255,0.2)",
        "board": "0 25px 60px rgba(0,0,0,0.8), 0 0 0 2px rgba(255,200,100,0.1)",
        "glass": "0 8px 32px rgba(0,0,0,0.4), inset 0 1px 0 rgba(255,255,255,0.1)",
        "glow-amber": "0 0 30px rgba(251,191,36,0.4)",
        "glow-crimson": "0 0 30px rgba(239,68,68,0.4)",
      },
      animation: {
        "dice-roll": "diceRoll 0.6s cubic-bezier(0.36, 0.07, 0.19, 0.97) both",
        "checker-bounce": "checkerBounce 0.4s cubic-bezier(0.36, 0.07, 0.19, 0.97)",
        "pulse-legal": "pulseLegal 1.5s ease-in-out infinite",
        "fade-in": "fadeIn 0.3s ease-out",
        "slide-up": "slideUp 0.4s cubic-bezier(0.16, 1, 0.3, 1)",
        "shimmer": "shimmer 2s linear infinite",
        "float": "float 6s ease-in-out infinite",
      },
      keyframes: {
        diceRoll: {
          "0%, 100%": { transform: "rotate(0deg) scale(1)" },
          "25%": { transform: "rotate(-15deg) scale(1.1)" },
          "75%": { transform: "rotate(15deg) scale(1.1)" },
        },
        checkerBounce: {
          "0%": { transform: "scale(1)" },
          "40%": { transform: "scale(1.15)" },
          "70%": { transform: "scale(0.95)" },
          "100%": { transform: "scale(1)" },
        },
        pulseLegal: {
          "0%, 100%": { opacity: "0.6", transform: "scale(1)" },
          "50%": { opacity: "1", transform: "scale(1.05)" },
        },
        fadeIn: {
          from: { opacity: "0" },
          to: { opacity: "1" },
        },
        slideUp: {
          from: { opacity: "0", transform: "translateY(20px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        shimmer: {
          "0%": { backgroundPosition: "-200% 0" },
          "100%": { backgroundPosition: "200% 0" },
        },
        float: {
          "0%, 100%": { transform: "translateY(0px)" },
          "50%": { transform: "translateY(-10px)" },
        },
      },
      borderRadius: {
        "4xl": "2rem",
      },
    },
  },
  plugins: [],
};
