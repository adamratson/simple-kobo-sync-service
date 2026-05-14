package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// handleLibraryState handles PUT /v1/library/{id}/state.
// The device uploads reading progress before and after sync; we must echo it back
// with a ReadingStateModified timestamp or the device retries indefinitely.
func (s *server) handleLibraryState(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		ReadingStates []json.RawMessage `json:"ReadingStates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.ReadingStates) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}
	var state map[string]any
	if err := json.Unmarshal(req.ReadingStates[0], &state); err != nil {
		state = map[string]any{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	state["EntitlementId"] = id
	state["ReadingStateModified"] = now
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

type authDeviceResponse struct {
	AccessToken  string `json:"AccessToken"`
	RefreshToken string `json:"RefreshToken"`
	TokenType    string `json:"TokenType"`
	TrackingId   string `json:"TrackingId"`
	UserKey      string `json:"UserKey"`
}

// staticUserKey is a fixed UUID used as the UserKey for all auth responses.
// The real Kobo API always returns UUID-formatted keys; we use a stable constant
// so the device's stored key never drifts from what we return.
const staticUserKey = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

// handleAuthDevice handles both /v1/auth/device and /v1/auth/refresh.
// We return stable fake tokens; downstream handlers don't validate them.
func (s *server) handleAuthDevice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(authDeviceResponse{
		AccessToken:  "kobo-sync-access-token",
		RefreshToken: "kobo-sync-refresh-token",
		TokenType:    "Bearer",
		TrackingId:   "00000000-0000-0000-0000-000000000000",
		UserKey:      staticUserKey,
	})
}

type userProfileResponse struct {
	UserId          string `json:"UserId"`
	UserKey         string `json:"UserKey"`
	UserDisplayName string `json:"UserDisplayName"`
	UserEmail       string `json:"UserEmail"`
	HasMembership   bool   `json:"HasMembership"`
	Memberships     []any  `json:"Memberships"`
}

func (s *server) handleUserProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userProfileResponse{
		UserId:          staticUserKey,
		UserKey:         staticUserKey,
		UserDisplayName: "Local User",
		UserEmail:       "user@local",
		HasMembership:   false,
		Memberships:     []any{},
	})
}

type oidcDiscoveryResponse struct {
	Issuer                           string   `json:"issuer"`
	AuthorizationEndpoint            string   `json:"authorization_endpoint"`
	TokenEndpoint                    string   `json:"token_endpoint"`
	UserinfoEndpoint                 string   `json:"userinfo_endpoint"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
}

func (s *server) handleOidcDiscovery(w http.ResponseWriter, r *http.Request) {
	b := s.baseURL()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(oidcDiscoveryResponse{
		Issuer:                           b,
		AuthorizationEndpoint:            b + "/oauth/authorize",
		TokenEndpoint:                    b + "/oauth/token",
		UserinfoEndpoint:                 b + "/oauth/userinfo",
		ResponseTypesSupported:           []string{"code"},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: []string{"RS256"},
	})
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	UserID       string `json:"user_id"`
	// PascalCase duplicates — some Kobo firmware variants expect these legacy names.
	AccessTokenAlt  string `json:"AccessToken"`
	RefreshTokenAlt string `json:"RefreshToken"`
	TokenTypeAlt    string `json:"TokenType"`
}

func (s *server) handleOAuth(w http.ResponseWriter, r *http.Request) {
	access := randomBase64(16)
	refresh := randomBase64(16)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(oauthTokenResponse{
		AccessToken:     access,
		RefreshToken:    refresh,
		TokenType:       "Bearer",
		ExpiresIn:       3600,
		Scope:           "",
		UserID:          staticUserKey,
		AccessTokenAlt:  access,
		RefreshTokenAlt: refresh,
		TokenTypeAlt:    "Bearer",
	})
}

func randomBase64(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "fallback-token"
	}
	return base64.StdEncoding.EncodeToString(b)
}

type syncItem struct {
	NewEntitlement *entitlement `json:"NewEntitlement,omitempty"`
}

type entitlement struct {
	BookEntitlement bookEntitlement `json:"BookEntitlement"`
	BookMetadata    bookMetadata    `json:"BookMetadata"`
	ReadingState    readingState    `json:"ReadingState"`
}

type readingState struct {
	Created           string          `json:"Created"`
	LastModified      string          `json:"LastModified"`
	PriorityTimestamp string          `json:"PriorityTimestamp"`
	EntitlementId     string          `json:"EntitlementId"`
	CurrentBookmark   currentBookmark `json:"CurrentBookmark"`
	Statistics        readingStats    `json:"Statistics"`
	StatusInfo        statusInfo      `json:"StatusInfo"`
}

type currentBookmark struct {
	ContentSourceProgressPercent float64 `json:"ContentSourceProgressPercent"`
	ProgressPercent              float64 `json:"ProgressPercent"`
	ChapterProgress              float64 `json:"ChapterProgress"`
}

type readingStats struct {
	SpentReadingMinutes    int `json:"SpentReadingMinutes"`
	RemainingTimeMinutes   int `json:"RemainingTimeMinutes"`
}

type statusInfo struct {
	LastModified        string `json:"LastModified"`
	Status              string `json:"Status"`
	TimesStartedReading int    `json:"TimesStartedReading"`
}

type bookEntitlement struct {
	Accessibility       string       `json:"Accessibility"`
	ActivePeriod        activePeriod `json:"ActivePeriod"`
	Created             string       `json:"Created"`
	CrossRevisionId     string       `json:"CrossRevisionId"`
	Id                  string       `json:"Id"`
	IsRemoved           bool         `json:"IsRemoved"`
	IsHiddenFromArchive bool         `json:"IsHiddenFromArchive"`
	IsLocked            bool         `json:"IsLocked"`
	LastModified        string       `json:"LastModified"`
	OriginCategory      string       `json:"OriginCategory"`
	RevisionId          string       `json:"RevisionId"`
	Status              string       `json:"Status"`
}

type activePeriod struct {
	From string `json:"From"`
}

type bookMetadata struct {
	Categories              []string          `json:"Categories"`
	CoverImageId            string            `json:"CoverImageId"`
	CrossRevisionId         string            `json:"CrossRevisionId"`
	CurrentDisplayPrice     bookPrice         `json:"CurrentDisplayPrice"`
	CurrentLoveDisplayPrice lovePrice         `json:"CurrentLoveDisplayPrice"`
	Description             *string           `json:"Description"`
	DownloadUrls            []syncDownloadURL `json:"DownloadUrls"`
	EntitlementId           string            `json:"EntitlementId"`
	ExternalIds             []string          `json:"ExternalIds"`
	Genre                   string            `json:"Genre"`
	IsEligibleForKoboLove   bool              `json:"IsEligibleForKoboLove"`
	IsInternetArchive       bool              `json:"IsInternetArchive"`
	IsPreOrder              bool              `json:"IsPreOrder"`
	IsSocialEnabled         bool              `json:"IsSocialEnabled"`
	Language                string            `json:"Language"`
	PhoneticPronunciations  map[string]any    `json:"PhoneticPronunciations"`
	PublicationDate         string            `json:"PublicationDate"`
	Publisher               bookPublisher     `json:"Publisher"`
	RevisionId              string            `json:"RevisionId"`
	Title                   string            `json:"Title"`
	WorkId                  string            `json:"WorkId"`
	ContributorRoles        []contributor     `json:"ContributorRoles"`
	Contributors            []string          `json:"Contributors"`
}

type syncDownloadURL struct {
	Format   string `json:"Format"`
	Size     int64  `json:"Size"`
	Url      string `json:"Url"`
	Platform string `json:"Platform"`
}

type bookPrice struct {
	CurrencyCode string `json:"CurrencyCode"`
	TotalAmount  int    `json:"TotalAmount"`
}

type lovePrice struct {
	TotalAmount int `json:"TotalAmount"`
}

type bookPublisher struct {
	Imprint string `json:"Imprint"`
	Name    string `json:"Name"`
}

func contributorNames(roles []contributor) []string {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names
}

type contributor struct {
	ContributorType string `json:"ContributorType"`
	Name            string `json:"Name"`
}

func (s *server) handleLibrarySync(w http.ResponseWriter, r *http.Request) {
	syncToken := r.Header.Get("x-kobo-synctoken")
	if syncToken == "" {
		syncToken = "initial"
	}

	books, err := scanEpubs(s.cfg.epubDir)
	if err != nil {
		slog.Warn("scan epubs failed", "err", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	items := make([]syncItem, 0, len(books))
	for _, book := range books {
		items = append(items, syncItem{NewEntitlement: s.buildEntitlement(book, now)})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-kobo-sync", "done")
	w.Header().Set("x-kobo-synctoken", syncToken)
	json.NewEncoder(w).Encode(items)
}

func (s *server) buildEntitlement(book epubMeta, now string) *entitlement {
	const genre = "00000000-0000-0000-0000-000000000001"
	roles := []contributor{}
	if book.Author != "" {
		roles = []contributor{{ContributorType: "Author", Name: book.Author}}
	}
	dl := s.baseURL() + "/v1/library/" + book.UUID + "/download"
	rs := readingState{
		Created:           now,
		LastModified:      now,
		PriorityTimestamp: now,
		EntitlementId:     book.UUID,
		CurrentBookmark:   currentBookmark{},
		Statistics:        readingStats{},
		StatusInfo: statusInfo{
			LastModified: now,
			Status:       "ReadyToRead",
		},
	}
	return &entitlement{
		ReadingState: rs,
		BookEntitlement: bookEntitlement{
			Accessibility:       "Full",
			ActivePeriod:        activePeriod{From: now},
			Created:             now,
			CrossRevisionId:     book.UUID,
			Id:                  book.UUID,
			IsRemoved:           false,
			IsHiddenFromArchive: false,
			IsLocked:            false,
			LastModified:        now,
			OriginCategory:      "Imported",
			RevisionId:          book.UUID,
			Status:              "Active",
		},
		BookMetadata: bookMetadata{
			Categories:              []string{genre},
			CoverImageId:            book.UUID,
			CrossRevisionId:         book.UUID,
			CurrentDisplayPrice:     bookPrice{CurrencyCode: "USD", TotalAmount: 0},
			CurrentLoveDisplayPrice: lovePrice{TotalAmount: 0},
			Description:             nil,
			DownloadUrls: []syncDownloadURL{
				{Format: "EPUB3", Size: book.Size, Url: dl, Platform: "Generic"},
				{Format: "EPUB", Size: book.Size, Url: dl, Platform: "Generic"},
			},
			EntitlementId:          book.UUID,
			ExternalIds:            []string{},
			Genre:                  genre,
			IsEligibleForKoboLove:  false,
			IsInternetArchive:      false,
			IsPreOrder:             false,
			IsSocialEnabled:        true,
			Language:               book.Language,
			PhoneticPronunciations: map[string]any{},
			PublicationDate:        now,
			Publisher:              bookPublisher{Imprint: "", Name: ""},
			RevisionId:             book.UUID,
			Title:                  book.Title,
			WorkId:                 book.UUID,
			ContributorRoles:       roles,
			Contributors:           contributorNames(roles),
		},
	}
}

func (s *server) handleLibraryMetadata(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	path, ok := findEpubByUUID(s.cfg.epubDir, id)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct{}{})
		return
	}
	meta, err := readEpubMeta(path)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct{}{})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	e := s.buildEntitlement(meta, now)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]bookMetadata{e.BookMetadata})
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	path, ok := findEpubByUUID(s.cfg.epubDir, id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "stat failed", http.StatusInternalServerError)
		return
	}
	filename := filepath.Base(path)
	w.Header().Set("Content-Type", "application/epub+zip")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	http.ServeContent(w, r, filename, fi.ModTime(), f)
}

// resources mirrors the JSON blob Kobo expects from GET /v1/initialization.
// Key names must match Calibre-Web's NATIVE_KOBO_RESOURCES exactly — the device
// looks up these string keys and silently fails if required ones are absent.
// Boolean feature-flag keys use string "false"; URL keys point at our server.
type resources struct {
	AccountPage                              string `json:"account_page"`
	AccountPageRakuten                       string `json:"account_page_rakuten"`
	AddDevice                                string `json:"add_device"`
	AddEntitlement                           string `json:"add_entitlement"`
	AffiliateRequest                         string `json:"affiliaterequest"`
	Assets                                   string `json:"assets"`
	Audiobook                                string `json:"audiobook"`
	AudiobookDetailPage                      string `json:"audiobook_detail_page"`
	AudiobookLandingPage                     string `json:"audiobook_landing_page"`
	AudiobookPreview                         string `json:"audiobook_preview"`
	AudiobookPurchaseWithCredit              string `json:"audiobook_purchase_withcredit"`
	AudiobookSubscriptionOrangeDeal          string `json:"audiobook_subscription_orange_deal_inclusion_url"`
	AuthorProductRecommendations             string `json:"authorproduct_recommendations"`
	Autocomplete                             string `json:"autocomplete"`
	BlackstoneHeader                         string `json:"blackstone_header"`
	Book                                     string `json:"book"`
	BookDetailPage                           string `json:"book_detail_page"`
	BookDetailPageRakuten                    string `json:"book_detail_page_rakuten"`
	BookLandingPage                          string `json:"book_landing_page"`
	BookSubscription                         string `json:"book_subscription"`
	BrowseHistory                            string `json:"browse_history"`
	Categories                               string `json:"categories"`
	CategoriesPage                           string `json:"categories_page"`
	Category                                 string `json:"category"`
	CategoryFeaturedLists                    string `json:"category_featured_lists"`
	CategoryProducts                         string `json:"category_products"`
	CheckoutBorrowedBook                     string `json:"checkout_borrowed_book"`
	ClientAuthdReferral                      string `json:"client_authd_referral"`
	ConfigurationData                        string `json:"configuration_data"`
	ContentAccessBook                        string `json:"content_access_book"`
	CustomerCareLiveChat                     string `json:"customer_care_live_chat"`
	DailyDeal                                string `json:"daily_deal"`
	Deals                                    string `json:"deals"`
	DeleteEntitlement                        string `json:"delete_entitlement"`
	DeleteTag                                string `json:"delete_tag"`
	DeleteTagItems                           string `json:"delete_tag_items"`
	DeviceAuth                               string `json:"device_auth"`
	DeviceRefresh                            string `json:"device_refresh"`
	DictionaryHost                           string `json:"dictionary_host"`
	DiscoveryHost                            string `json:"discovery_host"`
	EReaderDevices                           string `json:"ereaderdevices"`
	EulaPage                                 string `json:"eula_page"`
	ExchangeAuth                             string `json:"exchange_auth"`
	ExternalBook                             string `json:"external_book"`
	FacebookSSOPage                          string `json:"facebook_sso_page"`
	FeaturedList                             string `json:"featured_list"`
	FeaturedLists                            string `json:"featured_lists"`
	FreeBooks                                string `json:"free_books_page"`
	FteFeedback                              string `json:"fte_feedback"`
	FunnelMetrics                            string `json:"funnel_metrics"`
	GetDownloadKeys                          string `json:"get_download_keys"`
	GetDownloadLink                          string `json:"get_download_link"`
	GetTestsRequest                          string `json:"get_tests_request"`
	GiftcardEpdRedeemURL                     string `json:"giftcard_epd_redeem_url"`
	GiftcardRedeemURL                        string `json:"giftcard_redeem_url"`
	GpbFlowEnabled                           string `json:"gpb_flow_enabled"`
	HelpPage                                 string `json:"help_page"`
	ImageHost                                string `json:"image_host"`
	ImageURLQualityTemplate                  string `json:"image_url_quality_template"`
	ImageURLTemplate                         string `json:"image_url_template"`
	InstapaperEnabled                        string `json:"instapaper_enabled"`
	InstapaperEnvURL                         string `json:"instapaper_env_url"`
	InstapaperLinkAccountStart               string `json:"instapaper_link_account_start"`
	KoboAudiobooksCreditRedemption           string `json:"kobo_audiobooks_credit_redemption"`
	KoboAudiobooksEnabled                    string `json:"kobo_audiobooks_enabled"`
	KoboAudiobooksOrangeDealEnabled          string `json:"kobo_audiobooks_orange_deal_enabled"`
	KoboAudiobooksSubscriptionsEnabled       string `json:"kobo_audiobooks_subscriptions_enabled"`
	KoboDisplayPrice                         string `json:"kobo_display_price"`
	KoboDropboxLinkAccountEnabled            string `json:"kobo_dropbox_link_account_enabled"`
	KoboGoogleTax                            string `json:"kobo_google_tax"`
	KoboGoogleDriveLinkAccountEnabled        string `json:"kobo_googledrive_link_account_enabled"`
	KoboNativeBorrowEnabled                  string `json:"kobo_nativeborrow_enabled"`
	KoboOneDriveLinkAccountEnabled           string `json:"kobo_onedrive_link_account_enabled"`
	KoboOneStoreLibraryEnabled               string `json:"kobo_onestorelibrary_enabled"`
	KoboPrivacyCentreURL                     string `json:"kobo_privacyCentre_url"`
	KoboRedeemEnabled                        string `json:"kobo_redeem_enabled"`
	KoboShelfieEnabled                       string `json:"kobo_shelfie_enabled"`
	KoboSubscriptionsEnabled                 string `json:"kobo_subscriptions_enabled"`
	KoboSuperPointsEnabled                   string `json:"kobo_superpoints_enabled"`
	KoboWishlistEnabled                      string `json:"kobo_wishlist_enabled"`
	LibraryBook                              string `json:"library_book"`
	LibraryItems                             string `json:"library_items"`
	LibraryMetadata                          string `json:"library_metadata"`
	LibraryPrices                            string `json:"library_prices"`
	LibrarySearch                            string `json:"library_search"`
	LibrarySync                              string `json:"library_sync"`
	LoveDashboardPage                        string `json:"love_dashboard_page"`
	LovePointsRedemptionPage                 string `json:"love_points_redemption_page"`
	MagazineLandingPage                      string `json:"magazine_landing_page"`
	MoreSignInOptions                        string `json:"more_sign_in_options"`
	Notebooks                                string `json:"notebooks"`
	NotificationsRegistrationIssue           string `json:"notifications_registration_issue"`
	OAuthHost                                string `json:"oauth_host"`
	PasswordRetrievalPage                    string `json:"password_retrieval_page"`
	PersonalizedRecommendations              string `json:"personalizedrecommendations"`
	PocketLinkAccountStart                   string `json:"pocket_link_account_start"`
	PostAnalyticsEvent                       string `json:"post_analytics_event"`
	PPXPurchasingURL                         string `json:"ppx_purchasing_url"`
	PrivacyPage                              string `json:"privacy_page"`
	ProductNextRead                          string `json:"product_nextread"`
	ProductPrices                            string `json:"product_prices"`
	ProductRecommendations                   string `json:"product_recommendations"`
	ProductReviews                           string `json:"product_reviews"`
	Products                                 string `json:"products"`
	ProductsV2                               string `json:"productsv2"`
	ProviderExternalSignInPage               string `json:"provider_external_sign_in_page"`
	QuickbuyCheckout                         string `json:"quickbuy_checkout"`
	QuickbuyCreate                           string `json:"quickbuy_create"`
	RakutenTokenExchange                     string `json:"rakuten_token_exchange"`
	Rating                                   string `json:"rating"`
	ReadingServicesHost                      string `json:"reading_services_host"`
	ReadingState                             string `json:"reading_state"`
	RedeemInterstitialPage                   string `json:"redeem_interstitial_page"`
	RegistrationPage                         string `json:"registration_page"`
	RelatedItems                             string `json:"related_items"`
	RemainingBookSeries                      string `json:"remaining_book_series"`
	RenameTag                                string `json:"rename_tag"`
	Review                                   string `json:"review"`
	ReviewSentiment                          string `json:"review_sentiment"`
	ShelfieRecommendations                   string `json:"shelfie_recommendations"`
	SignInPage                               string `json:"sign_in_page"`
	SocialAuthorizationHost                  string `json:"social_authorization_host"`
	SocialHost                               string `json:"social_host"`
	StoreHome                                string `json:"store_home"`
	StoreHost                                string `json:"store_host"`
	StoreNewReleases                         string `json:"store_newreleases"`
	StoreSearch                              string `json:"store_search"`
	StoreTop50                               string `json:"store_top50"`
	SubsLandingPage                          string `json:"subs_landing_page"`
	SubsManagementPage                       string `json:"subs_management_page"`
	SubsPlansPage                            string `json:"subs_plans_page"`
	SubsPurchaseBuyTemplated                 string `json:"subs_purchase_buy_templated"`
	TagItems                                 string `json:"tag_items"`
	Tags                                     string `json:"tags"`
	TasteProfile                             string `json:"taste_profile"`
	TermsOfSalePage                          string `json:"terms_of_sale_page"`
	UpdateAccessibilityPreview               string `json:"update_accessibility_to_preview"`
	UseOneStore                              string `json:"use_one_store"`
	UserGuideHost                            string `json:"userguide_host"`
	UserLoyaltyBenefits                      string `json:"user_loyalty_benefits"`
	UserPlatform                             string `json:"user_platform"`
	UserProfile                              string `json:"user_profile"`
	UserRatings                              string `json:"user_ratings"`
	UserRecommendations                      string `json:"user_recommendations"`
	UserReviews                              string `json:"user_reviews"`
	UserWishlist                             string `json:"user_wishlist"`
	WishlistPage                             string `json:"wishlist_page"`
}

// initResponse wraps the resources blob the way Calibre-Web does.
// The device parses for a top-level "Resources" key; a flat object is silently ignored.
type initResponse struct {
	Resources resources `json:"Resources"`
}

func (s *server) handleInitialization(w http.ResponseWriter, r *http.Request) {
	b := s.baseURL()
	res := resources{
		AccountPage:                     b + "/account",
		AccountPageRakuten:              b + "/account/rakuten",
		AddDevice:                       b + "/v1/user/add-device",
		AddEntitlement:                  b + "/v1/library/{RevisionIds}",
		AffiliateRequest:                b + "/v1/affiliate",
		Assets:                          b + "/v1/assets",
		Audiobook:                       b + "/v1/products/audiobooks/{Id}",
		AudiobookDetailPage:             b + "/audiobooks/{Id}",
		AudiobookLandingPage:            b + "/audiobooks",
		AudiobookPreview:                b + "/v1/products/audiobooks/{Id}/preview",
		AudiobookPurchaseWithCredit:     b + "/v1/products/audiobooks/{Id}/purchase",
		AudiobookSubscriptionOrangeDeal: b + "/v1/audiobooks/subscription",
		AuthorProductRecommendations:    b + "/v1/products/books/authors/{AuthorId}/recommendations",
		Autocomplete:                    b + "/v1/products/autocomplete",
		BlackstoneHeader:                b + "/v1/blackstone",
		Book:                            b + "/v1/products/books/{Id}",
		BookDetailPage:                  b + "/books/{Id}",
		BookDetailPageRakuten:           b + "/books/{Id}/rakuten",
		BookLandingPage:                 b + "/books",
		BookSubscription:                b + "/v1/products/books/subscription/{Id}",
		BrowseHistory:                   b + "/v1/user/browsing-history",
		Categories:                      b + "/v1/categories",
		CategoriesPage:                  b + "/v1/categories/page/{PageNumber}/sort/{SortOrder}/kobo/json",
		Category:                        b + "/v1/categories/{Id}",
		CategoryFeaturedLists:           b + "/v1/categories/{Id}/featured",
		CategoryProducts:                b + "/v1/categories/{Id}/products",
		CheckoutBorrowedBook:            b + "/v1/library/{Id}/checkout",
		ClientAuthdReferral:             b + "/v1/auth/device",
		ConfigurationData:               b + "/v1/configuration",
		ContentAccessBook:               b + "/v1/products/books/{ProductId}/access",
		CustomerCareLiveChat:            b + "/support/chat",
		DailyDeal:                       b + "/v1/products/dailydeal",
		Deals:                           b + "/v1/deals",
		DeleteEntitlement:               b + "/v1/library/{Id}",
		DeleteTag:                       b + "/v1/library/tags/{Id}",
		DeleteTagItems:                  b + "/v1/library/tags/{Id}/items/delete",
		DeviceAuth:                      b + "/v1/auth/device",
		DeviceRefresh:                   b + "/v1/auth/refresh",
		DictionaryHost:                  b,
		DiscoveryHost:                   b,
		EReaderDevices:                  b + "/v1/devices",
		EulaPage:                        b + "/eula",
		ExchangeAuth:                    b + "/v1/auth/exchange",
		ExternalBook:                    b + "/v1/products/books/external/{Ids}",
		FacebookSSOPage:                 b + "/auth/facebook",
		FeaturedList:                    b + "/v1/products/featured/{FeaturedListId}",
		FeaturedLists:                   b + "/v1/products/featured",
		FreeBooks:                       b + "/free-books",
		FteFeedback:                     b + "/v1/feedback",
		FunnelMetrics:                   b + "/v1/metrics/funnel",
		GetDownloadKeys:                 b + "/v1/library/{Id}/download-keys",
		GetDownloadLink:                 b + "/v1/library/{Id}/download",
		GetTestsRequest:                 b + "/v1/analytics/tests",
		GiftcardEpdRedeemURL:            b + "/giftcard/redeem",
		GiftcardRedeemURL:               b + "/giftcard/redeem",
		GpbFlowEnabled:                  "false",
		HelpPage:                        b + "/help",
		ImageHost:                       b + "/images/",
		ImageURLQualityTemplate:         b + "/images/{ImageId}/{Width}/{Height}/false/image.jpg",
		ImageURLTemplate:                b + "/images/{ImageId}/{Width}/{Height}/false/image.jpg",
		InstapaperEnabled:               "false",
		InstapaperEnvURL:                b + "/v1/instapaper",
		InstapaperLinkAccountStart:      b + "/v1/instapaper/link",
		KoboAudiobooksCreditRedemption:  "false",
		KoboAudiobooksEnabled:           "false",
		KoboAudiobooksOrangeDealEnabled: "false",
		KoboAudiobooksSubscriptionsEnabled: "false",
		KoboDisplayPrice:                "false",
		KoboDropboxLinkAccountEnabled:   "false",
		KoboGoogleTax:                   "false",
		KoboGoogleDriveLinkAccountEnabled: "false",
		KoboNativeBorrowEnabled:         "false",
		KoboOneDriveLinkAccountEnabled:  "false",
		KoboOneStoreLibraryEnabled:      "false",
		KoboPrivacyCentreURL:            b + "/privacy",
		KoboRedeemEnabled:               "false",
		KoboShelfieEnabled:              "false",
		KoboSubscriptionsEnabled:        "false",
		KoboSuperPointsEnabled:          "false",
		KoboWishlistEnabled:             "false",
		LibraryBook:                     b + "/v1/user/library/books/{LibraryItemId}",
		LibraryItems:                    b + "/v1/user/library",
		LibraryMetadata:                 b + "/v1/library/{Ids}/metadata",
		LibraryPrices:                   b + "/v1/user/library/previews/prices",
		LibrarySearch:                   b + "/v1/library/search",
		LibrarySync:                     b + "/v1/library/sync",
		LoveDashboardPage:               b + "/love-dashboard",
		LovePointsRedemptionPage:        b + "/points/redeem",
		MagazineLandingPage:             b + "/magazines",
		MoreSignInOptions:               b + "/signin/options",
		Notebooks:                       b + "/v1/user/notebooks",
		NotificationsRegistrationIssue:  b + "/v1/notifications/register",
		OAuthHost:                       b,
		PasswordRetrievalPage:           b + "/account/reset-password",
		PersonalizedRecommendations:     b + "/v1/products/books/recommendations",
		PocketLinkAccountStart:          b + "/v1/pocket/link",
		PostAnalyticsEvent:              b + "/v1/analytics/event",
		PPXPurchasingURL:                b + "/v1/ppx/purchase",
		PrivacyPage:                     b + "/privacy",
		ProductNextRead:                 b + "/v1/products/{ProductId}/nextread",
		ProductPrices:                   b + "/v1/products/{Ids}/prices",
		ProductRecommendations:          b + "/v1/products/{ProductId}/recommendations",
		ProductReviews:                  b + "/v1/products/{ProductId}/reviews",
		Products:                        b + "/v1/products",
		ProductsV2:                      b + "/v2/products",
		ProviderExternalSignInPage:      b + "/signin/external",
		QuickbuyCheckout:                b + "/v1/quickbuy/checkout",
		QuickbuyCreate:                  b + "/v1/quickbuy/create",
		RakutenTokenExchange:            b + "/v1/auth/rakuten",
		Rating:                          b + "/v1/products/{ProductId}/rating/{Rating}",
		ReadingServicesHost:             b,
		ReadingState:                    b + "/v1/library/{Ids}/state",
		RedeemInterstitialPage:          b + "/redeem",
		RegistrationPage:                b + "/register",
		RelatedItems:                    b + "/v1/products/{Id}/related",
		RemainingBookSeries:             b + "/v1/products/books/series/{SeriesId}",
		RenameTag:                       b + "/v1/library/tags/{Id}/rename",
		Review:                          b + "/v1/products/{Id}/review",
		ReviewSentiment:                 b + "/v1/products/{Id}/review/sentiment",
		ShelfieRecommendations:          b + "/v1/products/books/shelfie",
		SignInPage:                      b + "/signin",
		SocialAuthorizationHost:         b,
		SocialHost:                      b,
		StoreHome:                       b + "/store",
		StoreHost:                       b,
		StoreNewReleases:                b + "/store/new-releases",
		StoreSearch:                     b + "/store/search",
		StoreTop50:                      b + "/v1/products/top-downloads",
		SubsLandingPage:                 b + "/subscriptions",
		SubsManagementPage:              b + "/subscriptions/manage",
		SubsPlansPage:                   b + "/subscriptions/plans",
		SubsPurchaseBuyTemplated:        b + "/v1/subscriptions/purchase/{PlanId}",
		TagItems:                        b + "/v1/library/tags/{TagId}/items",
		Tags:                            b + "/v1/library/tags",
		TasteProfile:                    b + "/v1/products/books/taste-profile",
		TermsOfSalePage:                 b + "/terms",
		UpdateAccessibilityPreview:      b + "/v1/library/tags/{TagId}/items/{ItemIds}/accessibility/preview",
		UseOneStore:                     "False",
		UserGuideHost:                   b,
		UserLoyaltyBenefits:             b + "/v1/user/loyalty/benefits",
		UserPlatform:                    b + "/v1/user/platform",
		UserProfile:                     b + "/v1/user/profile",
		UserRatings:                     b + "/v1/user/ratings",
		UserRecommendations:             b + "/v1/user/recommendations",
		UserReviews:                     b + "/v1/user/reviews",
		UserWishlist:                    b + "/v1/user/wishlist",
		WishlistPage:                    b + "/v1/products/{ProductId}/wishlist",
	}

	w.Header().Set("Content-Type", "application/json")
	// e30= is base64({}) — Calibre-Web always sets this; some firmware versions check for it.
	w.Header().Set("x-kobo-apitoken", "e30=")
	slog.Info("initialization", "library_sync", res.LibrarySync)
	json.NewEncoder(w).Encode(initResponse{Resources: res})
}

// baseURL returns the external base URL plus token prefix for embedding in resource URLs.
// Uses the configured externalURL rather than r.Host — the Kobo omits the port from
// the Host header, which would cause generated URLs to point at port 80 instead of ours.
func (s *server) baseURL() string {
	return s.cfg.externalURL + "/kobo/" + s.cfg.token
}
