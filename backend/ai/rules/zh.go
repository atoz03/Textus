package rules

// Chinese rules: the region-neutral ones for both scripts, and the mainland
// (GB/T) ones. Messages are in Chinese, in the script of the text.

// zhm is a message in the text's script.
func zhm(c *ctx, hans, hant string) string {
	if c.lang == ZhHant {
		return hant
	}
	return hans
}

func zhRun(t *Text, c *ctx, o *out) {
	mainland := c.profile.mainland()
	zhHalfwidth(t, c, o)
	zhFullwidthAdjacent(t, c, o)
	zhEllipsis(t, c, o)
	zhEllipsisDeng(t, c, o)
	zhStacked(t, c, o)
	zhHeadingEnd(t, c, o)
	zhCJKLatin(t, c, o)
	zhWidthAlnum(t, c, o)
	zhDecimal(t, c, o)
	zhMixedDate(t, c, o)
	zhShortYear(t, c, o)
	zhDateDunhao(t, c, o)
	zhTypo(t, c, o)
	zhUnitSpace(t, c, o)
	zhASCIIRange(t, c, o)
	zhCompatIdeograph(t, c, o)
	zhClauseLength(t, c, o)
	if mainland {
		zhDash(t, c, o)
		zhQuoteStraight(t, c, o)
		zhApprox(t, c, o)
		zhTitleDunhao(t, c, o)
		zhMixedUnit(t, c, o)
		zhMiddot(t, c, o)
		zhGongwen(t, c, o)
		zhAd(t, c, o)
	}
	if c.lang == ZhHans {
		zhVariant(t, c, o)
		zhScriptMixed(t, c, o)
	}
	if c.lang == ZhHant {
		twRun(t, c, o)
	}
}
