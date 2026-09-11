import { theme } from "antd";
import type { ThemeConfig } from "antd";
import type { ThemeMode } from "../stores/uiStore";

/** 品牌色：金融蓝；红涨绿跌沿用 A 股习惯。 */
export const BRAND = {
  primary: "#2563eb",
  up: "#cf1322", // 涨
  down: "#3f8600", // 跌
  flat: "#8c8c8c",
};

/** 全局主题令牌：品牌色 + 统一圆角 + 明暗两套算法（AntD theme token 定制）。 */
export function themeConfig(mode: ThemeMode): ThemeConfig {
  const dark = mode === "dark";
  return {
    algorithm: dark ? theme.darkAlgorithm : theme.defaultAlgorithm,
    token: {
      colorPrimary: BRAND.primary,
      borderRadius: 8,
      fontSize: 14,
      colorBgLayout: dark ? "#0d1117" : "#f5f7fa",
      wireframe: false,
    },
    components: {
      Layout: {
        headerBg: dark ? "#11161d" : "#ffffff",
        siderBg: dark ? "#11161d" : "#001f45",
        bodyBg: dark ? "#0d1117" : "#f5f7fa",
      },
      Menu: { darkItemBg: "transparent", darkSubMenuItemBg: "transparent" },
      Card: { borderRadiusLG: 12 },
      Table: { headerBg: dark ? "#161c24" : "#fafbfc" },
    },
  };
}
