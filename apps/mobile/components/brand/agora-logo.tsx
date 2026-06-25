/**
 * Agora wordmark / sigil. 1:1 vector copy of docs/assets/logo-light.svg —
 * keep this file and the SVG in sync.
 *
 * react-native-svg does not resolve CSS `currentColor`, so callers must pass
 * `color` explicitly. For theme-aware usage, pair with `useColorScheme` +
 * `THEME` token from `@/lib/theme`.
 */
import Svg, { Circle } from "react-native-svg";
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
      <Circle cx="50" cy="50" r="29" fill="none" stroke={resolvedColor} strokeWidth={1.5} opacity={0.3} />
      <Circle cx="50" cy="21" r="8.5" fill={resolvedColor} />
      <Circle cx="75.1" cy="64.5" r="8.5" fill={resolvedColor} />
      <Circle cx="24.9" cy="64.5" r="8.5" fill={resolvedColor} />
      <Circle cx="75.1" cy="35.5" r="7" fill="none" stroke={resolvedColor} strokeWidth={3.5} />
      <Circle cx="50" cy="79" r="7" fill="none" stroke={resolvedColor} strokeWidth={3.5} />
      <Circle cx="24.9" cy="35.5" r="7" fill="none" stroke={resolvedColor} strokeWidth={3.5} />
      <Circle cx="50" cy="50" r="10.5" fill="#2347E8" />
    </Svg>
  );
}
