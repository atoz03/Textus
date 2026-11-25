package rules

// Word and character lists for the Chinese rules: our own small,
// high-precision lists of the common unambiguous pairs, rather than
// OpenCC's large dictionaries. Generated from the Rust tables.

// pairKind is how a Simplified/Traditional pair converts.
type pairKind int

const (
	// pairBoth is one-to-one both ways.
	pairBoth pairKind = iota
	// pairSimpOnly: the Simplified character exists only in Simplified
	// text, but stands for several Traditional ones (发 -> 發/髮): flagged in
	// Traditional text without a single suggestion.
	pairSimpOnly
	// pairTradOnly: the Traditional character exists only in Traditional
	// text, but its Simplified form is also a Traditional character (後 ->
	// 后, 裡 -> 里): flagged in Simplified text only.
	pairTradOnly
)

type scriptPair struct {
	s, t rune
	kind pairKind
}

// scriptPairs are every pair, Simplified first.
var scriptPairs = []scriptPair{
	{'们', '們', pairBoth}, {'这', '這', pairBoth}, {'个', '個', pairBoth}, {'说', '說', pairBoth},
	{'为', '為', pairBoth}, {'学', '學', pairBoth}, {'国', '國', pairBoth}, {'时', '時', pairBoth},
	{'会', '會', pairBoth}, {'来', '來', pairBoth}, {'对', '對', pairBoth}, {'经', '經', pairBoth},
	{'过', '過', pairBoth}, {'还', '還', pairBoth}, {'见', '見', pairBoth}, {'长', '長', pairBoth},
	{'门', '門', pairBoth}, {'问', '問', pairBoth}, {'开', '開', pairBoth}, {'关', '關', pairBoth},
	{'买', '買', pairBoth}, {'卖', '賣', pairBoth}, {'东', '東', pairBoth}, {'车', '車', pairBoth},
	{'电', '電', pairBoth}, {'话', '話', pairBoth}, {'体', '體', pairBoth}, {'点', '點', pairBoth},
	{'变', '變', pairBoth}, {'让', '讓', pairBoth}, {'从', '從', pairBoth}, {'与', '與', pairBoth},
	{'实', '實', pairBoth}, {'现', '現', pairBoth}, {'观', '觀', pairBoth}, {'应', '應', pairBoth},
	{'该', '該', pairBoth}, {'样', '樣', pairBoth}, {'间', '間', pairBoth}, {'题', '題', pairBoth},
	{'结', '結', pairBoth}, {'论', '論', pairBoth}, {'议', '議', pairBoth}, {'认', '認', pairBoth},
	{'识', '識', pairBoth}, {'区', '區', pairBoth}, {'页', '頁', pairBoth}, {'写', '寫', pairBoth},
	{'读', '讀', pairBoth}, {'书', '書', pairBoth}, {'图', '圖', pairBoth}, {'数', '數', pairBoth},
	{'据', '據', pairBoth}, {'计', '計', pairBoth}, {'试', '試', pairBoth}, {'验', '驗', pairBoth},
	{'钱', '錢', pairBoth}, {'业', '業', pairBoth}, {'产', '產', pairBoth}, {'统', '統', pairBoth},
	{'级', '級', pairBoth}, {'网', '網', pairBoth}, {'络', '絡', pairBoth}, {'报', '報', pairBoth},
	{'爱', '愛', pairBoth}, {'边', '邊', pairBoth}, {'宾', '賓', pairBoth}, {'补', '補', pairBoth},
	{'层', '層', pairBoth}, {'场', '場', pairBoth}, {'尘', '塵', pairBoth}, {'陈', '陳', pairBoth},
	{'称', '稱', pairBoth}, {'处', '處', pairBoth}, {'传', '傳', pairBoth}, {'闯', '闖', pairBoth},
	{'词', '詞', pairBoth}, {'达', '達', pairBoth}, {'带', '帶', pairBoth}, {'单', '單', pairBoth},
	{'党', '黨', pairBoth}, {'导', '導', pairBoth}, {'灯', '燈', pairBoth}, {'敌', '敵', pairBoth},
	{'递', '遞', pairBoth}, {'动', '動', pairBoth}, {'断', '斷', pairBoth}, {'队', '隊', pairBoth},
	{'吨', '噸', pairBoth}, {'夺', '奪', pairBoth}, {'儿', '兒', pairBoth}, {'飞', '飛', pairBoth},
	{'费', '費', pairBoth}, {'风', '風', pairBoth}, {'妇', '婦', pairBoth}, {'盖', '蓋', pairBoth},
	{'赶', '趕', pairBoth}, {'刚', '剛', pairBoth}, {'钢', '鋼', pairBoth}, {'给', '給', pairBoth},
	{'贡', '貢', pairBoth}, {'构', '構', pairBoth}, {'购', '購', pairBoth}, {'顾', '顧', pairBoth},
	{'广', '廣', pairBoth}, {'归', '歸', pairBoth}, {'贵', '貴', pairBoth}, {'汉', '漢', pairBoth},
	{'号', '號', pairBoth}, {'华', '華', pairBoth}, {'画', '畫', pairBoth}, {'坏', '壞', pairBoth},
	{'欢', '歡', pairBoth}, {'环', '環', pairBoth}, {'换', '換', pairBoth}, {'机', '機', pairBoth},
	{'积', '積', pairBoth}, {'极', '極', pairBoth}, {'际', '際', pairBoth}, {'济', '濟', pairBoth},
	{'继', '繼', pairBoth}, {'坚', '堅', pairBoth}, {'简', '簡', pairBoth}, {'将', '將', pairBoth},
	{'讲', '講', pairBoth}, {'奖', '獎', pairBoth}, {'节', '節', pairBoth}, {'进', '進', pairBoth},
	{'惊', '驚', pairBoth}, {'镜', '鏡', pairBoth}, {'旧', '舊', pairBoth}, {'举', '舉', pairBoth},
	{'剧', '劇', pairBoth}, {'决', '決', pairBoth}, {'军', '軍', pairBoth}, {'课', '課', pairBoth},
	{'块', '塊', pairBoth}, {'况', '況', pairBoth}, {'兰', '蘭', pairBoth}, {'览', '覽', pairBoth},
	{'劳', '勞', pairBoth}, {'乐', '樂', pairBoth}, {'类', '類', pairBoth}, {'礼', '禮', pairBoth},
	{'丽', '麗', pairBoth}, {'联', '聯', pairBoth}, {'两', '兩', pairBoth}, {'辆', '輛', pairBoth},
	{'疗', '療', pairBoth}, {'领', '領', pairBoth}, {'刘', '劉', pairBoth}, {'龙', '龍', pairBoth},
	{'楼', '樓', pairBoth}, {'录', '錄', pairBoth}, {'陆', '陸', pairBoth}, {'虑', '慮', pairBoth},
	{'乱', '亂', pairBoth}, {'罗', '羅', pairBoth}, {'妈', '媽', pairBoth}, {'马', '馬', pairBoth},
	{'吗', '嗎', pairBoth}, {'满', '滿', pairBoth}, {'梦', '夢', pairBoth}, {'难', '難', pairBoth},
	{'脑', '腦', pairBoth}, {'鸟', '鳥', pairBoth}, {'农', '農', pairBoth}, {'欧', '歐', pairBoth},
	{'盘', '盤', pairBoth}, {'齐', '齊', pairBoth}, {'气', '氣', pairBoth}, {'桥', '橋', pairBoth},
	{'亲', '親', pairBoth}, {'庆', '慶', pairBoth}, {'权', '權', pairBoth}, {'劝', '勸', pairBoth},
	{'确', '確', pairBoth}, {'热', '熱', pairBoth}, {'荣', '榮', pairBoth}, {'伤', '傷', pairBoth},
	{'设', '設', pairBoth}, {'声', '聲', pairBoth}, {'胜', '勝', pairBoth}, {'师', '師', pairBoth},
	{'视', '視', pairBoth}, {'适', '適', pairBoth}, {'势', '勢', pairBoth}, {'树', '樹', pairBoth},
	{'双', '雙', pairBoth}, {'谁', '誰', pairBoth}, {'顺', '順', pairBoth}, {'丝', '絲', pairBoth},
	{'岁', '歲', pairBoth}, {'孙', '孫', pairBoth}, {'态', '態', pairBoth}, {'汤', '湯', pairBoth},
	{'讨', '討', pairBoth}, {'条', '條', pairBoth}, {'铁', '鐵', pairBoth}, {'听', '聽', pairBoth},
	{'厅', '廳', pairBoth}, {'头', '頭', pairBoth}, {'卫', '衛', pairBoth}, {'无', '無', pairBoth},
	{'务', '務', pairBoth}, {'雾', '霧', pairBoth}, {'戏', '戲', pairBoth}, {'细', '細', pairBoth},
	{'吓', '嚇', pairBoth}, {'线', '線', pairBoth}, {'乡', '鄉', pairBoth}, {'响', '響', pairBoth},
	{'协', '協', pairBoth}, {'谢', '謝', pairBoth}, {'兴', '興', pairBoth}, {'选', '選', pairBoth},
	{'寻', '尋', pairBoth}, {'压', '壓', pairBoth}, {'亚', '亞', pairBoth}, {'严', '嚴', pairBoth},
	{'颜', '顏', pairBoth}, {'阳', '陽', pairBoth}, {'养', '養', pairBoth}, {'药', '藥', pairBoth},
	{'爷', '爺', pairBoth}, {'医', '醫', pairBoth}, {'仪', '儀', pairBoth}, {'艺', '藝', pairBoth},
	{'亿', '億', pairBoth}, {'义', '義', pairBoth}, {'译', '譯', pairBoth}, {'阴', '陰', pairBoth},
	{'银', '銀', pairBoth}, {'营', '營', pairBoth}, {'优', '優', pairBoth}, {'犹', '猶', pairBoth},
	{'邮', '郵', pairBoth}, {'鱼', '魚', pairBoth}, {'语', '語', pairBoth}, {'园', '園', pairBoth},
	{'员', '員', pairBoth}, {'远', '遠', pairBoth}, {'约', '約', pairBoth}, {'运', '運', pairBoth},
	{'杂', '雜', pairBoth}, {'灾', '災', pairBoth}, {'载', '載', pairBoth}, {'战', '戰', pairBoth},
	{'张', '張', pairBoth}, {'账', '賬', pairBoth}, {'针', '針', pairBoth}, {'证', '證', pairBoth},
	{'郑', '鄭', pairBoth}, {'职', '職', pairBoth}, {'纸', '紙', pairBoth}, {'质', '質', pairBoth},
	{'众', '眾', pairBoth}, {'猪', '豬', pairBoth}, {'专', '專', pairBoth}, {'转', '轉', pairBoth},
	{'装', '裝', pairBoth}, {'状', '狀', pairBoth}, {'资', '資', pairBoth}, {'总', '總', pairBoth},
	{'组', '組', pairBoth}, {'钻', '鑽', pairBoth}, {'价', '價', pairBoth}, {'击', '擊', pairBoth},
	{'软', '軟', pairBoth}, {'发', '發', pairSimpOnly}, {'历', '歷', pairSimpOnly},
	{'当', '當', pairSimpOnly}, {'复', '復', pairSimpOnly}, {'汇', '匯', pairSimpOnly},
	{'获', '獲', pairSimpOnly}, {'尽', '盡', pairSimpOnly}, {'团', '團', pairSimpOnly},
	{'赞', '讚', pairSimpOnly}, {'钟', '鐘', pairSimpOnly}, {'坛', '壇', pairSimpOnly},
	{'脏', '髒', pairSimpOnly}, {'后', '後', pairTradOnly}, {'里', '裡', pairTradOnly},
	{'里', '裏', pairTradOnly}, {'面', '麵', pairTradOnly}, {'干', '幹', pairTradOnly},
	{'松', '鬆', pairTradOnly}, {'只', '隻', pairTradOnly}, {'发', '髮', pairTradOnly},
	{'历', '曆', pairTradOnly}, {'复', '複', pairTradOnly}, {'汇', '彙', pairTradOnly},
	{'获', '穫', pairTradOnly}, {'尽', '儘', pairTradOnly}, {'团', '糰', pairTradOnly},
	{'赞', '贊', pairTradOnly}, {'钟', '鍾', pairTradOnly}, {'云', '雲', pairTradOnly},
	{'台', '臺', pairTradOnly}, {'台', '颱', pairTradOnly}, {'台', '檯', pairTradOnly},
	{'范', '範', pairTradOnly}, {'伙', '夥', pairTradOnly}, {'几', '幾', pairTradOnly},
	{'万', '萬', pairTradOnly}, {'叶', '葉', pairTradOnly}, {'愿', '願', pairTradOnly},
	{'杰', '傑', pairTradOnly}, {'准', '準', pairTradOnly}, {'周', '週', pairTradOnly},
	{'种', '種', pairTradOnly}, {'系', '係', pairTradOnly}, {'系', '繫', pairTradOnly},
	{'么', '麼', pairTradOnly}, {'苹', '蘋', pairTradOnly}, {'丰', '豐', pairTradOnly},
	{'划', '劃', pairTradOnly}, {'当', '噹', pairTradOnly}, {'脏', '臟', pairTradOnly},
}

// zhTypos are wrong-character pairs, Simplified and Traditional. 莫明其妙
// is a suggestion only (in zhVariants): some dictionaries list it.
var zhTypos = [][2]string{
	{"再接再励", "再接再厉"},
	{"迫不急待", "迫不及待"},
	{"按步就班", "按部就班"},
	{"走头无路", "走投无路"},
	{"甘败下风", "甘拜下风"},
	{"默守成规", "墨守成规"},
	{"谈笑风声", "谈笑风生"},
	{"金榜提名", "金榜题名"},
	{"震憾", "震撼"},
	{"寒喧", "寒暄"},
	{"针贬", "针砭"},
	{"松驰", "松弛"},
	{"脉博", "脉搏"},
	{"峻工", "竣工"},
	{"气慨", "气概"},
	{"幅射", "辐射"},
	{"编篡", "编纂"},
	{"膺品", "赝品"},
	{"一如继往", "一如既往"},
	{"美仑美奂", "美轮美奂"},
	{"不径而走", "不胫而走"},
	{"名信片", "明信片"},
	{"布署", "部署"},
	{"侯车", "候车"},
	{"一愁莫展", "一筹莫展"},
	{"黄梁", "黄粱"},
	{"再接再勵", "再接再厲"},
	{"走頭無路", "走投無路"},
	{"甘敗下風", "甘拜下風"},
	{"默守成規", "墨守成規"},
	{"談笑風聲", "談笑風生"},
	{"金榜提名", "金榜題名"},
	{"針貶", "針砭"},
	{"鬆馳", "鬆弛"},
	{"脈博", "脈搏"},
	{"氣慨", "氣概"},
	{"編篡", "編纂"},
	{"膺品", "贗品"},
	{"一如繼往", "一如既往"},
	{"美侖美奐", "美輪美奐"},
	{"不徑而走", "不脛而走"},
	{"佈署", "部署"},
	{"侯車", "候車"},
	{"一愁莫展", "一籌莫展"},
	{"黃梁", "黃粱"},
}

// zhVariants are variant words (异形词, GF 1001): (variant, recommended).
// Not 蹚/趟 or 噘/撅 (蹚 and 噘 are standard now) and not 标识.
var zhVariants = [][2]string{
	{"案语", "按语"},
	{"班驳", "斑驳"},
	{"辈份", "辈分"},
	{"补钉", "补丁"},
	{"参预", "参与"},
	{"车箱", "车厢"},
	{"成份", "成分"},
	{"筹画", "筹划"},
	{"出谋画策", "出谋划策"},
	{"磁器", "瓷器"},
	{"粗卤", "粗鲁"},
	{"搭当", "搭档"},
	{"搭挡", "搭档"},
	{"答覆", "答复"},
	{"耽心", "担心"},
	{"担搁", "耽搁"},
	{"倒楣", "倒霉"},
	{"雕零", "凋零"},
	{"雕谢", "凋谢"},
	{"跌荡", "跌宕"},
	{"定单", "订单"},
	{"定婚", "订婚"},
	{"定货", "订货"},
	{"定阅", "订阅"},
	{"端五", "端午"},
	{"份量", "分量"},
	{"份内", "分内"},
	{"份外", "分外"},
	{"丰富多采", "丰富多彩"},
	{"伏侍", "服侍"},
	{"告戒", "告诫"},
	{"梗直", "耿直"},
	{"恭惟", "恭维"},
	{"勾划", "勾画"},
	{"骨董", "古董"},
	{"股分", "股份"},
	{"关连", "关联"},
	{"光采", "光彩"},
	{"归根结柢", "归根结底"},
	{"过份", "过分"},
	{"含胡", "含糊"},
	{"喝采", "喝彩"},
	{"哄动", "轰动"},
	{"宏扬", "弘扬"},
	{"胡涂", "糊涂"},
	{"恍忽", "恍惚"},
	{"火伴", "伙伴"},
	{"机伶", "机灵"},
	{"计画", "计划"},
	{"记念", "纪念"},
	{"佳宾", "嘉宾"},
	{"架式", "架势"},
	{"简炼", "简练"},
	{"狡滑", "狡猾"},
	{"精采", "精彩"},
	{"刻划", "刻画"},
	{"狼籍", "狼藉"},
	{"累坠", "累赘"},
	{"联接", "连接"},
	{"连结", "联结"},
	{"留连", "流连"},
	{"卤莽", "鲁莽"},
	{"录象", "录像"},
	{"录相", "录像"},
	{"马糊", "马虎"},
	{"门坎", "门槛"},
	{"摹仿", "模仿"},
	{"模胡", "模糊"},
	{"磨擦", "摩擦"},
	{"那末", "那么"},
	{"内哄", "内讧"},
	{"凝炼", "凝练"},
	{"钮扣", "纽扣"},
	{"飘泊", "漂泊"},
	{"飘流", "漂流"},
	{"人材", "人才"},
	{"日蚀", "日食"},
	{"入坐", "入座"},
	{"塌实", "踏实"},
	{"透澈", "透彻"},
	{"图象", "图像"},
	{"推委", "推诿"},
	{"无动于中", "无动于衷"},
	{"希罕", "稀罕"},
	{"消弥", "消弭"},
	{"压韵", "押韵"},
	{"惟一", "唯一"},
	{"元来", "原来"},
	{"帐本", "账本"},
	{"折衷", "折中"},
	{"指手划脚", "指手画脚"},
	{"装璜", "装潢"},
	{"左证", "佐证"},
	{"莫明其妙", "莫名其妙"},
}

// twMainland are mainland words in Traditional text, suggestions for
// Taiwan. 質量 (also "mass") and 程序 (also "procedure") only in compounds.
var twMainland = [][2]string{
	{"軟件", "軟體"},
	{"硬件", "硬體"},
	{"信息", "資訊"},
	{"網絡", "網路"},
	{"視頻", "影片"},
	{"默認", "預設"},
	{"鼠標", "滑鼠"},
	{"打印", "列印"},
	{"服務器", "伺服器"},
	{"數據庫", "資料庫"},
	{"應用程序", "應用程式"},
	{"程序員", "程式設計師"},
	{"出租車", "計程車"},
	{"博客", "部落格"},
	{"文件夾", "資料夾"},
	{"菜單", "選單"},
	{"屏幕", "螢幕"},
	{"激光", "雷射"},
	{"短信", "簡訊"},
	{"質量問題", "品質問題"},
	{"質量管理", "品質管理"},
}

// twLegal is 法律統一用字: (other form, the legal form).
var twLegal = [][2]string{
	{"盡量", "儘量"},
	{"公佈", "公布"},
	{"分佈", "分布"},
	{"佔", "占"},
	{"部份", "部分"},
	{"身份", "身分"},
	{"賬", "帳"},
	{"搜集", "蒐集"},
	{"取銷", "取消"},
	{"徹底", "澈底"},
	{"抵觸", "牴觸"},
	{"粘貼", "黏貼"},
	{"電錶", "電表"},
	{"水錶", "水表"},
}

// twHK are Taiwan / Hong Kong character variants: (Taiwan, Hong Kong).
var twHK = [][2]rune{{'裡', '裏'}, {'著', '着'}, {'線', '綫'}, {'衛', '衞'}}

// hanScripts counts t's characters that have a form in the other script:
// Simplified ones (hans) and Traditional ones (hant).
func hanScripts(t *Text) (hans, hant int) {
	for _, p := range scriptPairs {
		for _, ch := range t.Chars {
			if ch == p.s && p.kind != pairTradOnly {
				hans++
			}
			if ch == p.t && p.kind != pairSimpOnly {
				hant++
			}
		}
	}
	return hans, hant
}

// toSimplified is the Simplified form of a Traditional-only character.
func toSimplified(c rune) (rune, bool) {
	for _, p := range scriptPairs {
		if p.t == c && p.kind != pairSimpOnly {
			return p.s, true
		}
	}
	return 0, false
}

// toTraditional is the Traditional form(s) of a Simplified-only character;
// nil for none.
func toTraditional(c rune) []rune {
	var forms []rune
	for _, p := range scriptPairs {
		if p.s == c && p.kind != pairTradOnly {
			forms = append(forms, p.t)
		}
	}
	// The other readings of a one-to-many character.
	oneToMany := false
	for _, p := range scriptPairs {
		if p.s == c && p.kind == pairSimpOnly {
			oneToMany = true
		}
	}
	if oneToMany {
		for _, p := range scriptPairs {
			if p.s == c && p.kind == pairTradOnly {
				forms = append(forms, p.t)
			}
		}
	}
	return forms
}
