// FrameworkStyle.swift — framework icon + brand color, matched to mobile.
//
// Mirrors mobile/src/components/FrameworkIcon.tsx so a project reads the same on
// the TV as on the phone: same framework set, same authoritative brand colors.
// Mobile uses MaterialCommunityIcons glyphs (react, triangle, lightning-bolt,
// language-swift, a custom Flutter SVG); tvOS uses SF Symbols, so we pick the
// closest SF Symbol per framework but keep mobile's exact brand color.

import SwiftUI

struct FrameworkStyle {
    let symbol: String
    let color: Color

    /// Match mobile's FRAMEWORK_SPECS keys + colors. Falls back to a neutral box.
    static func of(_ framework: String?) -> FrameworkStyle {
        switch (framework ?? "").lowercased() {
        case "expo":
            return .init(symbol: "atom", color: Color(hex: 0xA78BFA))          // mobile: react / #A78BFA
        case "react-native", "reactnative", "rn", "react":
            return .init(symbol: "atom", color: Color(hex: 0x61DAFB))          // mobile: react / #61DAFB
        case "flutter":
            return .init(symbol: "bird.fill", color: Color(hex: 0x42A5F5))     // mobile: Flutter SVG / #42A5F5
        case "swift":
            return .init(symbol: "swift", color: Color(hex: 0xFA7343))         // mobile: language-swift / #FA7343
        case "kotlin":
            return .init(symbol: "k.square.fill", color: Color(hex: 0x7F52FF)) // mobile: language-kotlin / #7F52FF
        case "nextjs", "next":
            return .init(symbol: "triangle.fill", color: Color(hex: 0xFAFAFA)) // mobile: triangle / #FAFAFA
        case "vite":
            return .init(symbol: "bolt.fill", color: Color(hex: 0xFFC107))     // mobile: lightning-bolt / #FFC107
        case "remix", "astro", "svelte", "web":
            return .init(symbol: "globe", color: Color(hex: 0x94A3B8))
        default:
            return .init(symbol: "shippingbox.fill", color: Color(hex: 0x94A3B8))
        }
    }
}

extension Color {
    /// 0xRRGGBB literal → Color, so brand hexes read exactly like mobile's.
    init(hex: UInt32) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: 1
        )
    }
}
