package content

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CompanyStaff is one employee of a 運営/株式会社 with the derived job and
// income (レガシー: 総合能力値=Σパラ×(170+65)/20、収入=(基本給×10+総合×10)/4)。
type CompanyStaff struct {
	ID       int64          `json:"id"`
	Idx      int            `json:"idx"`
	Params   map[string]int `json:"params"`
	Job      string         `json:"job"`
	Sougou   int            `json:"sougou"`
	Income   int64          `json:"income"`
	EduLog   string         `json:"edu_log"`
	CanEduAt string         `json:"can_edu_at"` // 次に教育できる時刻(RFC3339、空=今すぐ可)
}

// CompanyView is a 運営(tuika=1)/株式会社(tuika=2) house as seen by a viewer.
type CompanyView struct {
	IsCompany   bool           `json:"is_company"`
	Kind        int            `json:"kind"` // 1=運営 2=株式会社
	OwnerName   string         `json:"owner_name"`
	Own         bool           `json:"own"`
	Officer     bool           `json:"officer"` // 閲覧者が役員(株式会社)か
	Officers    []string       `json:"officers"`
	StaffMax    int            `json:"staff_max"`
	TotalIncome int64          `json:"total_income"`
	Staff       []CompanyStaff `json:"staff"`
	// 教育UI用の定数(レガシー: 効率1/10、1pt=2万円、間隔1時間)。
	EduEfficiency int `json:"edu_efficiency"`
	EduFeePoint   int `json:"edu_fee_point"`
	EduIntervalMi int `json:"edu_interval_min"`
}

// staffJobSQL derives the best-paying job a staff qualifies for. 社員は身長170cm
// /体重65kg固定なのでBMI≒22.5として絞る。無資格は浮浪者(給与0)。
const staffJobSQL = `
	SELECT cs.id, cs.idx, cs.params, cs.edu_log, cs.last_edu_at,
	       COALESCE(best.name, '浮浪者'), COALESCE(best.salary, 0),
	       (SELECT COALESCE(SUM(v.value::int), 0) FROM jsonb_each_text(cs.params) v)
	FROM company_staff cs
	LEFT JOIN LATERAL (
	  SELECT j.name, j.salary FROM content_jobs j
	  WHERE j.enabled AND COALESCE(j.require_master, '') = ''
	    AND (j.bmi_min IS NULL OR j.bmi_min <= 22.5)
	    AND (j.bmi_max IS NULL OR j.bmi_max >= 22.5)
	    AND (j.height_min IS NULL OR j.height_min <= 170)
	    AND NOT EXISTS (
	      SELECT 1 FROM jsonb_array_elements(j.requirements) r
	      WHERE r->>'pred' = 'param_gte'
	        AND COALESCE((cs.params->>(r->>'param'))::int, 0) < (r->>'value')::int)
	  ORDER BY j.salary DESC LIMIT 1
	) best ON true
	WHERE cs.house_id = $1
	ORDER BY cs.idx`

// StaffIncome computes the per-staff income from salary and total params
// (レガシーjyob_machi2: (基本給×10 + 総合能力値×10)/4)。
func StaffIncome(salary int64, paramSum int) (sougou int, income int64) {
	// 総合能力値 = Σパラ × (身長170+体重65)/20 = Σ×11.75。
	sougou = paramSum * 235 / 20
	income = (salary*10 + int64(sougou)*10) / 4
	return sougou, income
}

// Company returns the 運営/株式会社 screen state of a house.
func (s *Service) Company(ctx context.Context, viewerID, houseID int64) (*CompanyView, error) {
	var (
		ownerID   int64
		tuika     int
		ownerName string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT h.owner_id, h.tuika, COALESCE(p.display_name, '')
		 FROM player_houses h LEFT JOIN players p ON p.id = h.owner_id
		 WHERE h.id = $1`, houseID).Scan(&ownerID, &tuika, &ownerName)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && tuika != 1 && tuika != 2) {
		return &CompanyView{IsCompany: false, Staff: []CompanyStaff{}, Officers: []string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load house: %w", err)
	}
	view := &CompanyView{
		IsCompany:     true,
		Kind:          tuika,
		OwnerName:     ownerName,
		Own:           viewerID == ownerID,
		Staff:         []CompanyStaff{},
		Officers:      []string{},
		EduEfficiency: 10,
		EduFeePoint:   20000,
		EduIntervalMi: 60,
	}
	view.StaffMax = 5
	if tuika == 2 {
		orows, err := s.pool.Query(ctx,
			`SELECT o.player_id, COALESCE(p.display_name, '') FROM company_officers o
			 LEFT JOIN players p ON p.id = o.player_id WHERE o.house_id = $1 ORDER BY o.joined_at`, houseID)
		if err != nil {
			return nil, fmt.Errorf("list officers: %w", err)
		}
		defer orows.Close()
		officers := 0
		for orows.Next() {
			var (
				pid  int64
				name string
			)
			if err := orows.Scan(&pid, &name); err != nil {
				return nil, fmt.Errorf("scan officer: %w", err)
			}
			if pid == viewerID {
				view.Officer = true
			}
			view.Officers = append(view.Officers, name)
			officers++
		}
		if err := orows.Err(); err != nil {
			return nil, err
		}
		view.StaffMax = 3 * (officers + 1)
	}
	rows, err := s.pool.Query(ctx, staffJobSQL, houseID)
	if err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			st       CompanyStaff
			lastEdu  *time.Time
			salary   int64
			paramSum int
		)
		if err := rows.Scan(&st.ID, &st.Idx, &st.Params, &st.EduLog, &lastEdu, &st.Job, &salary, &paramSum); err != nil {
			return nil, fmt.Errorf("scan staff: %w", err)
		}
		st.Sougou, st.Income = StaffIncome(salary, paramSum)
		if lastEdu != nil {
			next := lastEdu.Add(time.Hour)
			if time.Now().Before(next) {
				st.CanEduAt = next.Format(time.RFC3339)
			}
		}
		view.TotalIncome += st.Income
		view.Staff = append(view.Staff, st)
	}
	return view, rows.Err()
}
