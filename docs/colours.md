
```css
:root {

  /* metal */
  --hamr-metal-dark: #1F2326;
  --hamr-metal-mid: #3A3F45;
  --hamr-metal-light: #7A8188;
  --hamr-metal-steel: #B8BEC6;

  /* fire */
  --hamr-fire-yellow: #FFD166;
  --hamr-fire-orange: #FF8C32;
  --hamr-fire-ember: #FF5A1F;
  --hamr-fire-deep: #C92E0A;

  /* glow */
  --hamr-fire-glow: #FFB347;

}
```
```tailwind
export default {
  theme: {
    extend: {
      colors: {

        hamr: {

          metal: {
            dark: "#1F2326",
            mid: "#3A3F45",
            light: "#7A8188",
            steel: "#B8BEC6"
          },

          fire: {
            yellow: "#FFD166",
            orange: "#FF8C32",
            ember: "#FF5A1F",
            deep: "#C92E0A",
            glow: "#FFB347"
          }

        }

      }
    }
  }
}
```
