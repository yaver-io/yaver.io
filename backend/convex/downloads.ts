import { v } from "convex/values";
import { internalMutation, internalQuery } from "./_generated/server";

/** Generate a Convex upload URL for file storage. */
export const generateUploadUrl = internalMutation(async (ctx) => {
  return await ctx.storage.generateUploadUrl();
});

/** Record a new download entry after uploading a file. */
export const createDownload = internalMutation({
  args: {
    platform: v.union(
      v.literal("macos"),
      v.literal("windows"),
      v.literal("linux"),
      v.literal("android"),
      v.literal("ios")
    ),
    arch: v.string(),
    format: v.string(),
    version: v.string(),
    filename: v.string(),
    storageId: v.id("_storage"),
    size: v.number(),
  },
  handler: async (ctx, args) => {
    // Remove existing entry for same platform/arch/format
    const existing = await ctx.db
      .query("downloads")
      .withIndex("by_platform_arch_format", (q) =>
        q.eq("platform", args.platform).eq("arch", args.arch).eq("format", args.format)
      )
      .first();

    if (existing) {
      // Delete old storage file and record
      await ctx.storage.delete(existing.storageId);
      await ctx.db.delete(existing._id);
    }

    return await ctx.db.insert("downloads", {
      ...args,
      createdAt: Date.now(),
    });
  },
});

/** Delete a download entry and its storage file. */
export const deleteDownload = internalMutation({
  args: {
    platform: v.union(
      v.literal("macos"),
      v.literal("windows"),
      v.literal("linux"),
      v.literal("android"),
      v.literal("ios")
    ),
    arch: v.string(),
    format: v.string(),
  },
  handler: async (ctx, args) => {
    const existing = await ctx.db
      .query("downloads")
      .withIndex("by_platform_arch_format", (q) =>
        q.eq("platform", args.platform).eq("arch", args.arch).eq("format", args.format)
      )
      .first();
    if (!existing) return null;
    await ctx.storage.delete(existing.storageId);
    await ctx.db.delete(existing._id);
    return existing.filename;
  },
});

/** List all available downloads. */
export const listDownloads = internalQuery({
  args: {},
  handler: async (ctx) => {
    const downloads = await ctx.db.query("downloads").collect();
    const result = [];
    for (const d of downloads) {
      const url = await ctx.storage.getUrl(d.storageId);
      result.push({
        platform: d.platform,
        arch: d.arch,
        format: d.format,
        version: d.version,
        filename: d.filename,
        size: d.size,
        url,
      });
    }
    return result;
  },
});

/** Resolve a single download by platform, arch, and format. */
export const getDownload = internalQuery({
  args: {
    platform: v.union(
      v.literal("macos"),
      v.literal("windows"),
      v.literal("linux"),
      v.literal("android"),
      v.literal("ios")
    ),
    arch: v.string(),
    format: v.string(),
  },
  handler: async (ctx, args) => {
    const download = await ctx.db
      .query("downloads")
      .withIndex("by_platform_arch_format", (q) =>
        q.eq("platform", args.platform).eq("arch", args.arch).eq("format", args.format)
      )
      .first();

    if (!download) return null;

    const url = await ctx.storage.getUrl(download.storageId);
    if (!url) return null;

    return {
      platform: download.platform,
      arch: download.arch,
      format: download.format,
      version: download.version,
      filename: download.filename,
      size: download.size,
      url,
    };
  },
});
