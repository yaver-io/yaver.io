import { defineSchema, defineTable } from "convex/server";
import { v } from "convex/values";

export default defineSchema({
  users: defineTable({
    email: v.string(),
    name: v.optional(v.string()),
    avatarUrl: v.optional(v.string()),
    provider: v.string(), // "apple" | "google" | "microsoft" | "password"
    createdAt: v.number(),
  }).index("by_email", ["email"]),

  sessions: defineTable({
    userId: v.id("users"),
    token: v.string(),
    createdAt: v.number(),
    expiresAt: v.number(),
  }).index("by_token", ["token"]),

  recipes: defineTable({
    title: v.string(),
    cookTime: v.number(),
    rating: v.number(),
    category: v.union(
      v.literal("Quick"),
      v.literal("Healthy"),
      v.literal("Comfort"),
      v.literal("Dessert"),
    ),
    imageUrl: v.optional(v.string()),
    ingredients: v.array(
      v.object({
        name: v.string(),
        amount: v.string(),
        price: v.optional(v.number()),
      }),
    ),
    steps: v.array(
      v.object({
        text: v.string(),
        duration: v.optional(v.number()),
      }),
    ),
  }).index("by_category", ["category"]),
});
