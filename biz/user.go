package biz

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/cuigh/auxo/cache"
	"github.com/cuigh/auxo/errors"
	"github.com/cuigh/auxo/log"
	"github.com/cuigh/auxo/net/web"
	"github.com/cuigh/auxo/security/passwd"
	"github.com/cuigh/swirl/dao"
	"github.com/cuigh/swirl/misc"
	"github.com/cuigh/swirl/model"
	"github.com/go-ldap/ldap"
)

var ErrIncorrectAuth = errors.New("login name or password is incorrect")

var User = &userBiz{}

type userBiz struct {
}

func (b *userBiz) GetByID(id string) (user *model.User, err error) {
	do(func(d dao.Interface) {
		user, err = d.UserGetByID(id)
	})
	return
}

func (b *userBiz) GetByName(loginName string) (user *model.User, err error) {
	do(func(d dao.Interface) {
		user, err = d.UserGetByName(loginName)
	})
	return
}

func (b *userBiz) Create(user *model.User, ctxUser web.User) (err error) {
	user.ID = misc.NewID()
	user.Status = model.UserStatusActive
	user.CreatedAt = time.Now()
	user.UpdatedAt = user.CreatedAt
	if user.Type == model.UserTypeInternal {
		user.Password, user.Salt, err = passwd.Generate(user.Password)
		if err != nil {
			return
		}
	}

	do(func(d dao.Interface) {
		if err = d.UserCreate(user); err == nil && ctxUser != nil {
			Event.CreateUser(model.EventActionCreate, user.LoginName, user.Name, ctxUser)
		}
	})
	return
}

func (b *userBiz) Update(user *model.User, ctxUser web.User) (err error) {
	do(func(d dao.Interface) {
		user.UpdatedAt = time.Now()
		if err = d.UserUpdate(user); err == nil {
			Event.CreateUser(model.EventActionUpdate, user.LoginName, user.Name, ctxUser)
		}
	})
	return
}

func (b *userBiz) Block(id string) (err error) {
	do(func(d dao.Interface) {
		err = d.UserBlock(id, true)
	})
	return
}

func (b *userBiz) Unblock(id string) (err error) {
	do(func(d dao.Interface) {
		err = d.UserBlock(id, false)
	})
	return
}

func (b *userBiz) Delete(id string) (err error) {
	do(func(d dao.Interface) {
		err = d.UserDelete(id)
	})
	return
}

func (b *userBiz) UpdateInfo(user *model.User) (err error) {
	do(func(d dao.Interface) {
		err = d.ProfileUpdateInfo(user)
	})
	return
}

func (b *userBiz) UpdatePassword(id, oldPwd, newPwd string) (err error) {
	do(func(d dao.Interface) {
		var (
			user      *model.User
			pwd, salt string
		)

		user, err = d.UserGetByID(id)
		if err != nil {
			return
		}

		if !passwd.Validate(oldPwd, user.Password, user.Salt) {
			err = errors.New("Current password is incorrect")
			return
		}

		pwd, salt, err = passwd.Generate(newPwd)
		if err != nil {
			return
		}

		err = d.ProfileUpdatePassword(id, pwd, salt)
	})
	return
}

func (b *userBiz) List(args *model.UserListArgs) (users []*model.User, count int, err error) {
	do(func(d dao.Interface) {
		users, count, err = d.UserList(args)
	})
	return
}

func (b *userBiz) Count() (count int, err error) {
	do(func(d dao.Interface) {
		count, err = d.UserCount()
	})
	return
}

func (b *userBiz) Login(name, pwd string) (token string, err error) {
	do(func(d dao.Interface) {
		var (
			user *model.User
		)

		user, err = d.UserGetByName(name)
		if err != nil {
			return
		}

		if user == nil {
			user = &model.User{
				Type:      model.UserTypeLDAP,
				LoginName: name,
			}
			err = b.loginLDAP(d, user, pwd)
		} else {
			if user.Status == model.UserStatusBlocked {
				err = fmt.Errorf("user %s is blocked", name)
				return
			}

			if user.Type == model.UserTypeInternal {
				err = b.loginInternal(user, pwd)
			} else {
				err = b.loginLDAP(d, user, pwd)
			}
		}

		if err != nil {
			return
		}

		session := &model.Session{
			UserID:    user.ID,
			Token:     misc.NewID(),
			UpdatedAt: time.Now(),
		}
		session.Expires = session.UpdatedAt.Add(time.Hour * 24)
		err = d.SessionUpdate(session)
		if err != nil {
			return
		}
		token = session.Token

		// create event
		Event.CreateAuthentication(model.EventActionLogin, user.ID, user.LoginName, user.Name)
	})
	return
}

func (b *userBiz) loginInternal(user *model.User, pwd string) error {
	if !passwd.Validate(pwd, user.Password, user.Salt) {
		return ErrIncorrectAuth
	}
	return nil
}

func (b *userBiz) loginLDAP(d dao.Interface, user *model.User, pwd string) error {
	setting, err := Setting.Get()
	if err != nil {
		return err
	}

	if !setting.LDAP.Enabled {
		return ErrIncorrectAuth
	}

	l, err := b.ldapDial(setting)
	if err != nil {
		return err
	}
	defer l.Close()

	// bind
	err = b.ldapBind(setting, l, user, pwd)
	if err != nil {
		log.Get("user").Error("LDAP > Failed to bind: ", err)
		return ErrIncorrectAuth
	}

	// Stop here for an exist user because we only need validate password.
	if user.ID != "" {
		return nil
	}

	// If user wasn't exist, we need create it
	entry, err := b.ldapSearchOne(setting, l, user.LoginName, setting.LDAP.NameAttr, setting.LDAP.EmailAttr)
	if err != nil {
		return err
	}

	user.Email = entry.GetAttributeValue(setting.LDAP.EmailAttr)
	user.Name = entry.GetAttributeValue(setting.LDAP.NameAttr)
	return b.Create(user, nil)
}

func (b *userBiz) ldapDial(setting *model.Setting) (*ldap.Conn, error) {
	host, _, err := net.SplitHostPort(setting.LDAP.Address)
	if err != nil {
		return nil, err
	}

	// TODO: support tls cert and verification
	tc := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true,
		Certificates:       nil,
	}

	if setting.LDAP.Security == model.LDAPSecurityTLS {
		return ldap.DialTLS("tcp", setting.LDAP.Address, tc)
	}

	conn, err := ldap.Dial("tcp", setting.LDAP.Address)
	if err != nil {
		return nil, err
	}

	if setting.LDAP.Security == model.LDAPSecurityStartTLS {
		if err = conn.StartTLS(tc); err != nil {
			conn.Close()
			log.Get("user").Error("LDAP > Failed to switch to TLS: ", err)
			return nil, err
		}
	}
	return conn, nil
}

func (b *userBiz) ldapBind(setting *model.Setting, l *ldap.Conn, user *model.User, pwd string) (err error) {
	if setting.LDAP.Authentication == 0 {
		// simple auth
		err = l.Bind(fmt.Sprintf(setting.LDAP.UserDN, user.LoginName), pwd)
	} else {
		// bind auth
		err = l.Bind(setting.LDAP.BindDN, setting.LDAP.BindPassword)
		if err == nil {
			var entry *ldap.Entry
			entry, err = b.ldapSearchOne(setting, l, user.LoginName, "cn")
			if err == nil {
				err = l.Bind(entry.DN, pwd)
			}
		}
	}
	return
}

func (b *userBiz) ldapSearchOne(setting *model.Setting, l *ldap.Conn, name string, attrs ...string) (entry *ldap.Entry, err error) {
	filter := fmt.Sprintf(setting.LDAP.UserFilter, name)
	req := ldap.NewSearchRequest(
		setting.LDAP.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		filter, attrs, nil,
	)
	sr, err := l.Search(req)
	if err != nil {
		return nil, err
	}

	if length := len(sr.Entries); length == 0 {
		return nil, errors.New("User not found with filter: " + filter)
	} else if length > 1 {
		return nil, errors.New("Found more than one account when searching user with filter: " + filter)
	}

	return sr.Entries[0], nil
}

// Identify authenticate user
func (b *userBiz) Identify(token string) (user web.User) {
	const cacheKey = "auth_user"

	do(func(d dao.Interface) {
		var (
			roles []*model.Role
			role  *model.Role
		)

		session, err := d.SessionGet(token)
		if err != nil {
			log.Get("user").Errorf("Load session failed: %v", err)
			return
		}
		if session == nil || session.Expires.Before(time.Now()) {
			return
		}

		value := cache.Get(cacheKey, session.UserID)
		if !value.IsNil() {
			user = &model.AuthUser{}
			if err = value.Scan(user); err == nil {
				return
			}
			log.Get("user").Warnf("Load auth user from cache failed: %v", err)
		}

		u, err := d.UserGetByID(session.UserID)
		if err != nil {
			log.Get("user").Errorf("Load user failed: %v", err)
			return
		}
		if u == nil {
			return
		}

		if len(u.Roles) > 0 {
			roles = make([]*model.Role, len(u.Roles))
			for i, id := range u.Roles {
				role, err = d.RoleGet(id)
				if err != nil {
					return
				} else if role != nil {
					roles[i] = role
				}
			}
		}
		user = model.NewAuthUser(u, roles)
		cache.Set(user, cacheKey, session.UserID)
	})
	return
}

// Authorize check permission of user
func (b *userBiz) Authorize(user web.User, h web.HandlerInfo) bool {
	if au, ok := user.(*model.AuthUser); ok {
		return au.IsAllowed(h.Name())
	}
	return false
}
