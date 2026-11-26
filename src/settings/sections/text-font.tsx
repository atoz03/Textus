import { useFontChoices } from "./font-choices";
import type { Strings } from "../../i18n/strings";
import {
  DOC_LETTER_SPACINGS,
  DOC_LINE_SPACINGS,
  DOC_TEXT_SIZES,
  LETTER_SPACING_BY,
  type DocLetterSpacing,
  type DocLineSpacing,
  type DocTextSize,
} from "../../prefs/SettingsContext";
import type { SettingsPageProps } from "../pages/types";
import { letterSpacingBy } from "../../prefs/settings-normalise";
import { Select } from "../../engine/controls/Select";
import { SettingRow } from "./controls";
import { ChevronIcon } from "../../engine/controls/icons";
import { NumberField } from "../../engine/controls/NumberField";
import { fill } from "../../i18n/fill";

/** The reader's own size, spacing and font for the document, over the
 *  theme's: folded under one row that says whether any is set. */

const LINE_SPACING_LABELS: Record<DocLineSpacing, keyof Strings> = {
  auto: "docLineSpacingAuto",
  compact: "docLineSpacingCompact",
  standard: "docLineSpacingStandard",
  relaxed: "docLineSpacingRelaxed",
};

const SPACING_LABELS: Record<DocLetterSpacing, keyof Strings> = {
  auto: "docLetterSpacingAuto",
  normal: "docLetterSpacingNormal",
  expanded: "docLetterSpacingExpanded",
  condensed: "docLetterSpacingCondensed",
};

type Props = Pick<SettingsPageProps, "t" | "settings" | "setSettings">;

export function DocTextFold({ t, settings, setSettings }: Props) {
  const custom =
    settings.docTextSize !== "auto" ||
    settings.docLineSpacing !== "auto" ||
    settings.docFont !== "" ||
    settings.docLetterSpacing !== "auto";
  return (
    <details className="settings-fold">
      <summary className="settings-row">
        <span className="settings-row-label">{t.docTextLabel}</span>
        <span className="settings-row-value">
          {custom ? t.docTextCustom : t.docTextSizeAuto}
        </span>
        <ChevronIcon className="settings-fold-chevron" />
      </summary>
      <div className="settings-fold-body">
        <FontRow t={t} settings={settings} setSettings={setSettings} />
        <SettingRow label={t.docTextSizeLabel}>
          <Select
            className="settings-select"
            aria-label={t.docTextSizeLabel}
            value={String(settings.docTextSize)}
            onChange={(e) =>
              setSettings({
                docTextSize: (e.target.value === "auto"
                  ? "auto"
                  : Number(e.target.value)) as DocTextSize,
              })
            }
          >
            {DOC_TEXT_SIZES.map((size) => (
              <option key={size} value={String(size)}>
                {size === "auto" ? t.docTextSizeAuto : `${size} px`}
              </option>
            ))}
          </Select>
        </SettingRow>
        <LetterSpacingRow t={t} settings={settings} setSettings={setSettings} />
        <SettingRow label={t.docLineSpacingLabel}>
          <Select
            className="settings-select"
            aria-label={t.docLineSpacingLabel}
            value={settings.docLineSpacing}
            onChange={(e) =>
              setSettings({
                docLineSpacing: e.target.value as DocLineSpacing,
              })
            }
          >
            {DOC_LINE_SPACINGS.map((spacing) => (
              <option key={spacing} value={spacing}>
                {t[LINE_SPACING_LABELS[spacing]] as string}
              </option>
            ))}
          </Select>
        </SettingRow>
      </div>
    </details>
  );
}

/** A font picked from the installed ones: `auto` keeps the default, then
 *  well-made faces, then the rest. */
function FontSelectRow({
  label,
  auto,
  moreLabel,
  language,
  value,
  onChange,
}: {
  label: string;
  auto: string;
  moreLabel: string;
  language: string;
  value: string;
  onChange: (font: string) => void;
}) {
  const { recommended, more } = useFontChoices(language);
  // A font chosen on another machine still shows as the choice.
  const chosenElsewhere =
    value && ![...recommended, ...more].some((c) => c.value === value);
  return (
    <SettingRow label={label}>
      <Select
        className="settings-select"
        aria-label={label}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">{auto}</option>
        {chosenElsewhere && <option value={value}>{value}</option>}
        {recommended.map(({ value, label }) => (
          <option key={value} value={value}>
            {label}
          </option>
        ))}
        {more.length > 0 && (
          <optgroup label={moreLabel}>
            {more.map(({ value, label }) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </optgroup>
        )}
      </Select>
    </SettingRow>
  );
}

function FontRow({ t, settings, setSettings }: Props) {
  return (
    <FontSelectRow
      label={t.docFontLabel}
      auto={t.docFontAuto}
      moreLabel={t.docFontMore}
      language={settings.language}
      value={settings.docFont}
      onChange={(docFont) => setSettings({ docFont })}
    />
  );
}

/** Word-style character spacing: expanded or condensed by a number of pt. */
function LetterSpacingRow({ t, settings, setSettings }: Props) {
  const adjustable =
    settings.docLetterSpacing === "expanded" ||
    settings.docLetterSpacing === "condensed";
  return (
    <SettingRow label={t.docLetterSpacingLabel}>
      <div className="settings-letter-spacing">
        <Select
          className="settings-select"
          aria-label={t.docLetterSpacingLabel}
          value={settings.docLetterSpacing}
          onChange={(e) =>
            setSettings({
              docLetterSpacing: e.target.value as DocLetterSpacing,
            })
          }
        >
          {DOC_LETTER_SPACINGS.map((spacing) => (
            <option key={spacing} value={spacing}>
              {t[SPACING_LABELS[spacing]] as string}
            </option>
          ))}
        </Select>
        {adjustable && (
          <NumberField
            label={t.docLetterSpacingBy}
            decreaseLabel={fill(t.numberDecrease, {
              label: t.docLetterSpacingLabel,
            })}
            increaseLabel={fill(t.numberIncrease, {
              label: t.docLetterSpacingLabel,
            })}
            value={settings.docLetterSpacingBy}
            placeholder={settings.docLetterSpacingBy}
            min={LETTER_SPACING_BY.min}
            max={LETTER_SPACING_BY.max}
            step={LETTER_SPACING_BY.step}
            unit="pt"
            onChange={(by) =>
              by !== null &&
              setSettings({ docLetterSpacingBy: letterSpacingBy(by) })
            }
          />
        )}
      </div>
    </SettingRow>
  );
}

/** The app's own text - menus, panels, Settings - in a font of the
 *  user's, over the system's; the document keeps its own (FontRow). */
export function UiFontRow({ t, settings, setSettings }: Props) {
  return (
    <FontSelectRow
      label={t.uiFontLabel}
      auto={t.uiFontAuto}
      moreLabel={t.docFontMore}
      language={settings.language}
      value={settings.uiFont}
      onChange={(uiFont) => setSettings({ uiFont })}
    />
  );
}
