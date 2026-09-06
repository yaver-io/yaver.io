import type { CodingSandbox, CodingTool } from "./sandboxTools";
import {
  YAVER_AGENT_TOOLS,
  dispatchYaverAgentTool,
  type YaverAgentToolContext,
} from "../yaverAgentTools";

export function makeYaverReadOnlyCodingTools(ctx: YaverAgentToolContext): CodingTool[] {
  return YAVER_AGENT_TOOLS.map((tool) => ({
    name: tool.name,
    description: tool.description,
    parameters: tool.parameters,
    mutating: false,
    async invoke(args: unknown, _box: CodingSandbox) {
      return dispatchYaverAgentTool(tool.name, args, ctx);
    },
  }));
}
