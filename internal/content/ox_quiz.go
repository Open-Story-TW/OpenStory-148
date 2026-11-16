package content

import (
	"fmt"
	"path/filepath"
	"strconv"
)

type OXQuestion struct {
	Set  byte
	ID   uint16
	Text string
	True bool
}

// The shipped client uses Big5 and cannot render the Korean quiz text in its
// archive. These questions retain their WZ set, ID, and answer key.
var oxQuestionTranslations = map[uint16]string{
	1:   "以前人們會用火盆取暖。",
	2:   "植物的莖能輸送根部吸收的水分。",
	3:   "本身會發光的物體叫作光源。",
	5:   "物體受光後呈現的深淺變化叫作明暗。",
	6:   "國家的三個構成要素是人民、主權和國防。",
	7:   "三代同堂的家庭叫作核心家庭。",
	17:  "夏天的夜晚比冬天長。",
	20:  "衣食住是指衣服、食物和娛樂。",
	21:  "空氣加熱後，體積會縮小。",
	22:  "色彩的明度是指顏色鮮豔的程度。",
	23:  "會吸血的是雄蚊。",
	104: "福爾摩斯與華生的故事是亞瑟柯南道爾寫的。",
	106: "諾貝爾獎含經濟學獎，共有六個獎項。",
}

// OXQuestions loads the answer key for questions with a client-readable
// translation and preserves their IDs for the native question window.
func (c *Catalog) OXQuestions() ([]OXQuestion, error) {
	file, err := readWZ(filepath.Join(c.root, "Etc.wz", "OXQuiz.img.xml"))
	if err != nil {
		return nil, err
	}
	var questions []OXQuestion
	for _, set := range file.Children {
		setID, err := strconv.ParseUint(set.Name, 10, 8)
		if err != nil || setID != 3 {
			continue
		}
		for _, q := range set.Children {
			questionID, err := strconv.ParseUint(q.Name, 10, 16)
			answer := q.intValue("a", -1)
			text := oxQuestionTranslations[uint16(questionID)]
			if err != nil || text == "" || q.stringValue("q", "") == "" || answer < 0 || answer > 1 {
				continue
			}
			questions = append(questions, OXQuestion{Set: byte(setID), ID: uint16(questionID), Text: text, True: answer == 1})
		}
	}
	if len(questions) < 10 {
		return nil, fmt.Errorf("OXQuiz needs at least ten questions")
	}
	return questions, nil
}
