import {
  SFMG_YAVER_APP,
  YAVER_FIRST_PARTY_GAMES as YAVER_FIRST_PARTY_GAME_APPS,
  getYaverAppBySlug,
  type YaverAppAiCapability,
  type YaverAppAuthContract,
  type YaverAppBillingMode,
  type YaverAppCategory,
  type YaverAppCommandContract,
  type YaverAppManifest,
  type YaverAppMonetizationContract,
  type YaverAppRuntimeMode,
  type YaverAppSurface,
} from "./yaver-apps";

export type YaverGameGenre = YaverAppCategory;
export type YaverGameSurface = YaverAppSurface;
export type YaverGameAuthMode = "required" | "optional" | "guest";
export type YaverGameBillingMode = YaverAppBillingMode;
export type YaverGameRuntimeMode = Extract<YaverAppRuntimeMode, "first-party" | "invited-developer" | "internal-tool">;
export type YaverGameAiCapability = YaverAppAiCapability;
export type YaverGameCommandContract = YaverAppCommandContract;
export type YaverGameAuthContract = YaverAppAuthContract;
export type YaverGameMonetizationContract = YaverAppMonetizationContract;
export type YaverGameManifest = YaverAppManifest & {
  readonly kind: "game";
  readonly genres: readonly YaverGameGenre[];
};

function asGameManifest(app: YaverAppManifest): YaverGameManifest {
  return {
    ...app,
    kind: "game",
    genres: app.categories,
  };
}

export const SFMG_YAVER_MANIFEST: YaverGameManifest = asGameManifest(SFMG_YAVER_APP);
export const YAVER_FIRST_PARTY_GAMES = YAVER_FIRST_PARTY_GAME_APPS.map(asGameManifest);

export function getYaverGameBySlug(slug: string): YaverGameManifest | undefined {
  const app = getYaverAppBySlug(slug);
  return app?.kind === "game" ? asGameManifest(app) : undefined;
}
