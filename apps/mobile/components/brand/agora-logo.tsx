/**
 * Agora wordmark / sigil. 1:1 vector copy of docs/assets/logo-light.svg —
 * keep this file and the SVG in sync.
 *
 * react-native-svg does not resolve CSS `currentColor`, so callers must pass
 * `color` explicitly. For theme-aware usage, pair with `useColorScheme` +
 * `THEME` token from `@/lib/theme`.
 */
import Svg, { Polygon } from "react-native-svg";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";

interface AgoraLogoProps {
  size?: number;
  color?: string;
}

export function AgoraLogo({ size = 48, color }: AgoraLogoProps) {
  const { isDarkColorScheme } = useColorScheme();
  const resolvedColor =
    color ?? (isDarkColorScheme ? THEME.dark.foreground : THEME.light.foreground);

  return (
    <Svg width={size} height={size} viewBox="0 0 100 100">
      <Polygon
        fill={resolvedColor}
        points="50,4 57.27,32.45 82.5,17.5 67.55,42.73 96,50 67.55,57.27 82.5,82.5 57.27,67.55 50,96 42.73,67.55 17.5,82.5 32.45,57.27 4,50 32.45,42.73 17.5,17.5 42.73,32.45"
      />
    </Svg>
  );
}
